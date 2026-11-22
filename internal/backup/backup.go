// Package backup 提供数据库与配置文件的备份、校验和恢复。
//
// 备份包是一个 ZIP 文件，包含 SQLite 数据库快照、config.yaml 与 manifest.json。
// 恢复时先校验上传内容，再写入 data/restore-pending，等待服务重启时应用，
// 避免运行中的进程与正在替换的数据库文件发生冲突。
package backup

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
	"lactssl/internal/config"
	"lactssl/internal/db"
	"lactssl/internal/util"
	_ "modernc.org/sqlite"
)

const (
	// DatabaseName 是 SQLite 备份包内数据库文件的固定名称。
	DatabaseName = "lactssl.db"
	// PostgresDatabaseName 是 PostgreSQL 逻辑备份在包内的固定名称。
	PostgresDatabaseName = "lactssl.dump"
	// MySQLDatabaseName 是 MySQL 逻辑备份在包内的固定名称。
	MySQLDatabaseName = "lactssl.sql"
	// driverName 记录待恢复目录中的数据库驱动。
	driverName = "driver"
	// ConfigName 是备份包内配置文件的固定名称。
	ConfigName = "config.yaml"
	// ManifestName 是备份包内元数据文件的固定名称。
	ManifestName = "manifest.json"
	// MaxUploadSize 限制恢复上传的大小，避免占用过多磁盘和内存。
	MaxUploadSize int64 = 256 << 20

	// 加密备份使用 PBKDF2-HMAC-SHA256 派生密钥，并以分块 AES-256-GCM 加密内部 ZIP。
	encryptionKDF        = "pbkdf2-sha256"
	encryptionCipher     = "aes-256-gcm"
	encryptionCipherV2   = "aes-256-gcm-chunked"
	encryptionIterations = 600000
	encryptionChunkSize  = 1 << 20
	encryptionSaltSize   = 16
	encryptionAAD        = "LACTSSL-BACKUP-V1"
	encryptionAADV2      = "LACTSSL-BACKUP-V2"

	pendingDirName = "restore-pending"
	maxConfigSize  = 1 << 20
	sqliteHeader   = "SQLite format 3\x00"
)

var (
	// ErrTooLarge 表示上传文件超过大小限制。
	ErrTooLarge = errors.New("备份文件过大")
	// ErrInvalidArchive 表示 ZIP 备份包结构不正确。
	ErrInvalidArchive = errors.New("备份包结构不正确")
	// ErrInvalidDatabase 表示数据库文件无效或损坏。
	ErrInvalidDatabase = errors.New("数据库文件无效")
	// ErrInvalidConfig 表示配置文件无法解析。
	ErrInvalidConfig = errors.New("配置文件无效")
	// ErrPasswordRequired 表示备份已加密但未提供密码。
	ErrPasswordRequired = errors.New("需要备份解密密码")
	// ErrInvalidPassword 表示备份解密失败，通常是密码不正确。
	ErrInvalidPassword = errors.New("备份解密密码不正确")
	// ErrKeyMismatch 表示备份数据库与配置文件的加密密钥不匹配。
	ErrKeyMismatch = errors.New("备份数据库与配置文件的加密密钥不匹配")
)

// requiredTables 是恢复时要求存在的基础表。
var requiredTables = []string{
	"users",
	"settings",
	"dns_accounts",
	"deploy_accounts",
	"private_cas",
	"certificates",
	"tasks",
	"deploy_records",
	"monitors",
	"logs",
}

// Manager 负责生成和校验备份。
type Manager struct {
	DB         *db.Handle
	DataDir    string
	DBPath     string
	ConfigFile string
	Version    string

	mu sync.Mutex
}

// New 创建备份管理器。
func New(conn *db.Handle, dataDir, dbPath, configFile, version string) *Manager {
	return &Manager{
		DB:         conn,
		DataDir:    dataDir,
		DBPath:     dbPath,
		ConfigFile: configFile,
		Version:    version,
	}
}

// Info 描述当前可备份的数据。
type Info struct {
	DatabaseSize   int64 `json:"database_size"`
	ConfigSize     int64 `json:"config_size"`
	ConfigIncluded bool  `json:"config_included"`
	PendingRestore bool  `json:"pending_restore"`
}

// Info 返回数据库大小、配置是否存在以及是否有待恢复的备份。
func (m *Manager) Info() (Info, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	info := Info{}
	if m.DB != nil && m.DB.Dialect().IsPostgres() {
		_ = m.DB.QueryRow(`SELECT pg_database_size(current_database())`).Scan(&info.DatabaseSize)
	} else if m.DB != nil && m.DB.Dialect().IsMySQL() {
		_ = m.DB.QueryRow(`SELECT COALESCE(SUM(data_length+index_length),0)
			FROM information_schema.tables WHERE table_schema=DATABASE()`).Scan(&info.DatabaseSize)
	} else if stat, err := os.Stat(m.DBPath); err == nil {
		info.DatabaseSize = stat.Size()
	}
	if stat, err := os.Stat(m.ConfigFile); err == nil && stat.Mode().IsRegular() {
		info.ConfigSize = stat.Size()
		info.ConfigIncluded = true
	}
	if _, err := os.Stat(PendingDir(m.DataDir)); err == nil {
		info.PendingRestore = true
	}
	return info, nil
}

type manifest struct {
	App        string    `json:"app"`
	Version    string    `json:"version"`
	Driver     string    `json:"driver,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	Database   string    `json:"database,omitempty"`
	Config     string    `json:"config,omitempty"`
	Encrypted  bool      `json:"encrypted,omitempty"`
	KDF        string    `json:"kdf,omitempty"`
	Iterations int       `json:"iterations,omitempty"`
	Salt       string    `json:"salt,omitempty"`
	Cipher     string    `json:"cipher,omitempty"`
	Payload    string    `json:"payload,omitempty"`
}

// Export 生成 ZIP 备份包并返回临时文件路径、建议文件名和清理函数。
// password 非空时先构建普通 ZIP，再用 AES-256-GCM 加密为 payload.enc 放入外层 ZIP。
func (m *Manager) Export(password string) (string, string, func(), error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	tempDir, err := os.MkdirTemp("", "lactssl-backup-")
	if err != nil {
		return "", "", func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(tempDir) }

	databaseName := DatabaseName
	if m.DB != nil && m.DB.Dialect().IsPostgres() {
		databaseName = PostgresDatabaseName
	} else if m.DB != nil && m.DB.Dialect().IsMySQL() {
		databaseName = MySQLDatabaseName
	}
	dbSnapshot := filepath.Join(tempDir, databaseName)
	if err := m.snapshot(dbSnapshot); err != nil {
		cleanup()
		return "", "", func() {}, err
	}

	plainPath := filepath.Join(tempDir, "backup.zip")
	meta := manifest{
		App:       "LACTSSL",
		Version:   m.Version,
		Driver:    "sqlite",
		CreatedAt: time.Now().UTC(),
		Database:  databaseName,
	}
	if databaseName == PostgresDatabaseName {
		meta.Driver = "postgres"
	} else if databaseName == MySQLDatabaseName {
		meta.Driver = "mysql"
	}
	if stat, err := os.Stat(m.ConfigFile); err == nil && stat.Mode().IsRegular() {
		meta.Config = ConfigName
	}
	if err := writePlainArchive(plainPath, dbSnapshot, m.ConfigFile, meta); err != nil {
		cleanup()
		return "", "", func() {}, err
	}
	// 数据库快照已写入 ZIP，及时释放临时空间。
	_ = os.Remove(dbSnapshot)

	result := plainPath
	if password != "" {
		encryptedPath, err := encryptArchive(plainPath, tempDir, meta, password)
		if err != nil {
			cleanup()
			return "", "", func() {}, err
		}
		_ = os.Remove(plainPath)
		result = encryptedPath
	}
	// 导出与恢复共用同一上传上限：超过上限的备份在灾难恢复时会被拒绝。
	if info, statErr := os.Stat(result); statErr == nil && info.Size() > MaxUploadSize {
		cleanup()
		return "", "", func() {}, fmt.Errorf("备份包大小 %d 字节超过恢复上限 %d 字节", info.Size(), MaxUploadSize)
	}

	filename := fmt.Sprintf("lactssl-backup-%s.zip", time.Now().Format("20060102-150405"))
	if password != "" {
		filename = fmt.Sprintf("lactssl-backup-%s-encrypted.zip", time.Now().Format("20060102-150405"))
	}
	return result, filename, cleanup, nil
}

// writePlainArchive 生成包含数据库、配置和元数据的普通 ZIP。
func writePlainArchive(target, dbSnapshot, configFile string, meta manifest) error {
	archive, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	zw := zip.NewWriter(archive)

	// 数据库快照压缩后通常很小，使用 Deflate 便于下载。
	name := meta.Database
	if name == "" {
		name = DatabaseName
	}
	if err := addFileToZip(zw, name, dbSnapshot); err != nil {
		_ = zw.Close()
		_ = archive.Close()
		return err
	}
	if meta.Config != "" {
		if err := addFileToZip(zw, ConfigName, configFile); err != nil {
			_ = zw.Close()
			_ = archive.Close()
			return err
		}
	}
	raw, _ := json.MarshalIndent(meta, "", "  ")
	header := &zip.FileHeader{Name: ManifestName, Method: zip.Deflate}
	header.SetMode(0o600)
	header.Modified = time.Now()
	writer, err := zw.CreateHeader(header)
	if err == nil {
		_, err = writer.Write(raw)
	}
	if closeErr := zw.Close(); err == nil {
		err = closeErr
	}
	if closeErr := archive.Close(); err == nil {
		err = closeErr
	}
	return err
}

// encryptArchive 将普通 ZIP 加密后写入外层 ZIP。
func encryptArchive(source, tempDir string, inner manifest, password string) (string, error) {
	salt := make([]byte, encryptionSaltSize)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return "", err
	}
	payload := filepath.Join(tempDir, "payload.enc")
	if err := encryptFile(source, payload, password, salt); err != nil {
		return "", err
	}

	meta := manifest{
		App:        inner.App,
		Version:    inner.Version,
		Driver:     inner.Driver,
		Database:   inner.Database,
		CreatedAt:  inner.CreatedAt,
		Encrypted:  true,
		KDF:        encryptionKDF,
		Iterations: encryptionIterations,
		Salt:       base64.StdEncoding.EncodeToString(salt),
		Cipher:     encryptionCipherV2,
		Payload:    "payload.enc",
	}
	encryptedPath := filepath.Join(tempDir, "encrypted.zip")
	archive, err := os.OpenFile(encryptedPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return "", err
	}
	zw := zip.NewWriter(archive)
	raw, _ := json.MarshalIndent(meta, "", "  ")
	header := &zip.FileHeader{Name: ManifestName, Method: zip.Deflate}
	header.SetMode(0o600)
	header.Modified = time.Now()
	writer, err := zw.CreateHeader(header)
	if err == nil {
		_, err = writer.Write(raw)
	}
	if err == nil {
		// payload 已经压缩并加密，外层不再重复压缩。
		err = addFileToZipWithMethod(zw, meta.Payload, payload, zip.Store)
	}
	if closeErr := zw.Close(); err == nil {
		err = closeErr
	}
	if closeErr := archive.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return "", err
	}
	_ = os.Remove(payload)
	return encryptedPath, nil
}

// chunkAAD 为每个分块生成唯一附加认证数据，防止分块被重排或截断。
func chunkAAD(index uint64) []byte {
	aad := make([]byte, 0, len(encryptionAADV2)+8)
	aad = append(aad, encryptionAADV2...)
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], index)
	return append(aad, buf[:]...)
}

// encryptFile 使用 PBKDF2 派生密钥，把文件分块加密为 length|nonce|ciphertext 结构。
func encryptFile(source, target, password string, salt []byte) error {
	key, err := pbkdf2.Key(sha256.New, password, salt, encryptionIterations, 32)
	if err != nil {
		return err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()

	buffer := make([]byte, encryptionChunkSize)
	for index := uint64(0); ; index++ {
		n, readErr := io.ReadFull(in, buffer)
		if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
			return readErr
		}
		if n > 0 {
			nonce := make([]byte, gcm.NonceSize())
			if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
				return err
			}
			if n > encryptionChunkSize {
				return fmt.Errorf("备份分块大小异常: %d", n)
			}
			ciphertext := gcm.Seal(nil, nonce, buffer[:n], chunkAAD(index))
			var length [4]byte
			binary.BigEndian.PutUint32(length[:], uint32(n))
			if _, err := out.Write(length[:]); err != nil {
				return err
			}
			if _, err := out.Write(nonce); err != nil {
				return err
			}
			if _, err := out.Write(ciphertext); err != nil {
				return err
			}
		}
		if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
			break
		}
	}
	if err := out.Sync(); err != nil {
		return err
	}
	return out.Close()
}

// decryptStream 解密上述分块格式，密码错误或数据被篡改时返回 ErrInvalidPassword。
// legacy 为 true 时兼容旧版备份（所有分块使用同一 AAD）。
func decryptStream(source io.Reader, target, password string, salt []byte, iterations int, legacy bool) error {
	if iterations <= 0 {
		iterations = encryptionIterations
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, iterations, 32)
	if err != nil {
		return err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()

	aad := []byte(encryptionAAD)
	var length [4]byte
	var total int64
	for index := uint64(0); ; index++ {
		_, err := io.ReadFull(source, length[:])
		if err == io.EOF {
			break
		}
		if err != nil {
			return ErrInvalidArchive
		}
		size := binary.BigEndian.Uint32(length[:])
		if size == 0 || size > encryptionChunkSize {
			return ErrInvalidArchive
		}
		total += int64(size)
		if total > MaxUploadSize+encryptionChunkSize {
			return ErrTooLarge
		}
		nonce := make([]byte, gcm.NonceSize())
		if _, err := io.ReadFull(source, nonce); err != nil {
			return ErrInvalidArchive
		}
		ciphertext := make([]byte, int(size)+gcm.Overhead())
		if _, err := io.ReadFull(source, ciphertext); err != nil {
			return ErrInvalidArchive
		}
		chunkAADValue := aad
		if !legacy {
			chunkAADValue = chunkAAD(index)
		}
		plain, err := gcm.Open(nil, nonce, ciphertext, chunkAADValue)
		if err != nil {
			return ErrInvalidPassword
		}
		if _, err := out.Write(plain); err != nil {
			return err
		}
	}
	if err := out.Sync(); err != nil {
		return err
	}
	return out.Close()
}

// snapshot 使用 VACUUM INTO 生成一致的 SQLite 快照，即使服务正在运行也安全。
func (m *Manager) snapshot(target string) error {
	if m.DB == nil {
		return errors.New("数据库未就绪")
	}
	if _, err := os.Stat(target); err == nil {
		return fmt.Errorf("备份目标文件已存在: %s", target)
	}
	if m.DB.Dialect().IsPostgres() {
		pgDump, err := findPgDump()
		if err != nil {
			return err
		}
		safeDSN, pgEnv := db.PostgresCLIEnv(m.DB.DSN())
		cmd := exec.CommandContext(context.Background(), pgDump, "--format=custom", "--no-owner", "--no-privileges", "--file", target, safeDSN) // #nosec G204 -- pg_dump 从 PATH 解析，参数由服务端生成
		cmd.Env = append(os.Environ(), pgEnv...)
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("pg_dump 失败: %w: %s", err, strings.TrimSpace(string(output)))
		}
		return nil
	}
	if m.DB.Dialect().IsMySQL() {
		mysqlDump, err := findMySQLDump()
		if err != nil {
			return err
		}
		connection, err := db.MySQLCLI(m.DB.DSN())
		if err != nil {
			return err
		}
		cmd := exec.CommandContext(context.Background(), mysqlDump, db.MySQLDumpArgs(mysqlDump, connection, target)...) // #nosec G204 -- mysqldump 从 PATH 解析，参数由服务端生成
		cmd.Env = append(os.Environ(), "MYSQL_PWD="+connection.Password)
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("mysqldump 失败: %w: %s", err, strings.TrimSpace(string(output)))
		}
		return nil
	}
	if _, err := m.DB.Exec(`VACUUM INTO ?`, target); err != nil {
		return fmt.Errorf("创建数据库快照失败: %w", err)
	}
	return nil
}

// addFileToZip 把本地文件写入 ZIP，备份文件不保留可执行权限。
func addFileToZip(zw *zip.Writer, name, source string) error {
	return addFileToZipWithMethod(zw, name, source, zip.Deflate)
}

func addFileToZipWithMethod(zw *zip.Writer, name, source string, method uint16) error {
	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	file, err := os.Open(source)
	if err != nil {
		return err
	}
	defer file.Close()

	header := &zip.FileHeader{Name: name, Method: method}
	header.SetMode(0o600)
	header.Modified = info.ModTime()
	writer, err := zw.CreateHeader(header)
	if err != nil {
		return err
	}
	_, err = io.Copy(writer, file)
	return err
}

// Stage 校验上传的备份并写入待恢复目录。
// 支持完整 ZIP 备份包、加密 ZIP 备份包和单独的 .db 数据库文件。
func (m *Manager) Stage(source io.Reader, filename, password string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.DataDir == "" {
		return errors.New("数据目录未配置")
	}
	if err := os.MkdirAll(m.DataDir, 0o700); err != nil {
		return fmt.Errorf("创建数据目录失败: %w", err)
	}
	workDir, err := os.MkdirTemp(m.DataDir, ".restore-upload-")
	if err != nil {
		return fmt.Errorf("创建临时目录失败: %w", err)
	}
	defer os.RemoveAll(workDir)

	uploadPath := filepath.Join(workDir, "upload.bin")
	if err := writeLimitedFile(uploadPath, source, MaxUploadSize); err != nil {
		return err
	}

	var (
		dbPath     string
		configPath string
		driver     = "sqlite"
	)
	reader, err := os.Open(uploadPath)
	if err != nil {
		return err
	}
	buffered := bufio.NewReader(reader)
	magic, _ := buffered.Peek(5)
	isZip := len(magic) >= 4 && bytes.Equal(magic[:4], []byte("PK\x03\x04"))
	_ = reader.Close()

	if isZip {
		meta, payload, err := readArchiveManifest(uploadPath)
		if meta != nil && meta.Driver != "" {
			driver = meta.Driver
		}
		if err != nil {
			return err
		}
		if meta != nil && meta.Encrypted {
			if password == "" {
				return ErrPasswordRequired
			}
			if (meta.Cipher != "" && meta.Cipher != encryptionCipher && meta.Cipher != encryptionCipherV2) || (meta.KDF != "" && meta.KDF != encryptionKDF) {
				return ErrInvalidArchive
			}
			salt, err := base64.StdEncoding.DecodeString(meta.Salt)
			if err != nil || len(salt) < 8 || len(salt) > 64 {
				return ErrInvalidArchive
			}
			iterations := meta.Iterations
			if iterations == 0 {
				iterations = encryptionIterations
			}
			// 限制迭代次数上限，避免未认证的恢复请求通过极高 KDF 参数消耗 CPU。
			if iterations < 10000 || iterations > 1000000 {
				return ErrInvalidArchive
			}
			innerPath := filepath.Join(workDir, "payload.zip")
			if err := decryptArchivePayload(uploadPath, payload, innerPath, password, salt, iterations, meta.Cipher != encryptionCipherV2); err != nil {
				return err
			}
			dbPath, configPath, err = extractArchive(innerPath, workDir)
		} else {
			dbPath, configPath, err = extractArchive(uploadPath, workDir)
		}
	} else {
		dbPath = uploadPath
		if bytes.HasPrefix(magic, []byte("PGDMP")) {
			driver = "postgres"
		}
		if strings.HasSuffix(strings.ToLower(filename), ".sql") || looksLikeMySQLDump(uploadPath) {
			driver = "mysql"
		}
	}
	if err != nil {
		return err
	}
	if dbPath == "" {
		return ErrInvalidArchive
	}
	if err := validateDatabase(driver, dbPath); err != nil {
		return err
	}
	if configPath != "" {
		if err := validateConfig(configPath); err != nil {
			return err
		}
		if driver == "sqlite" {
			if err := verifyBackupKeyPair(dbPath, configPath); err != nil {
				return err
			}
		}
	}

	pending := PendingDir(m.DataDir)
	next := pending + ".new"
	_ = os.RemoveAll(next)
	if err := os.MkdirAll(next, 0o700); err != nil {
		return fmt.Errorf("创建待恢复目录失败: %w", err)
	}
	databaseName := DatabaseName
	if driver == "postgres" {
		databaseName = PostgresDatabaseName
	}
	if driver == "mysql" {
		databaseName = MySQLDatabaseName
	}
	if err := copyFile(dbPath, filepath.Join(next, databaseName)); err != nil {
		_ = os.RemoveAll(next)
		return err
	}
	if err := os.WriteFile(filepath.Join(next, driverName), []byte(driver), 0o600); err != nil {
		_ = os.RemoveAll(next)
		return err
	}
	if configPath != "" {
		if err := copyFile(configPath, filepath.Join(next, ConfigName)); err != nil {
			_ = os.RemoveAll(next)
			return err
		}
	}
	// 先完整写入临时目录，再替换旧的待恢复内容，避免中途失败留下残缺备份。
	_ = os.RemoveAll(pending)
	if err := os.Rename(next, pending); err != nil {
		_ = os.RemoveAll(next)
		return fmt.Errorf("保存待恢复备份失败: %w", err)
	}
	return nil
}

// readArchiveManifest 读取备份包中的元数据，并定位加密 payload。
func readArchiveManifest(archivePath string) (*manifest, string, error) {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %v", ErrInvalidArchive, err)
	}
	defer reader.Close()

	var meta *manifest
	payload := ""
	for _, entry := range reader.File {
		if entry.FileInfo().IsDir() {
			continue
		}
		name := path.Base(strings.ReplaceAll(entry.Name, "\\", "/"))
		if strings.EqualFold(name, ManifestName) {
			file, err := entry.Open()
			if err != nil {
				return nil, "", fmt.Errorf("%w: %v", ErrInvalidArchive, err)
			}
			raw, err := io.ReadAll(io.LimitReader(file, maxConfigSize+1))
			_ = file.Close()
			if err != nil || len(raw) > maxConfigSize {
				return nil, "", ErrInvalidArchive
			}
			var value manifest
			if err := json.Unmarshal(raw, &value); err != nil {
				return nil, "", ErrInvalidArchive
			}
			meta = &value
		}
		if strings.EqualFold(name, "payload.enc") {
			payload = entry.Name
		}
	}
	if meta != nil && meta.Encrypted && payload == "" {
		payload = meta.Payload
		if payload == "" {
			return nil, "", ErrInvalidArchive
		}
	}
	return meta, payload, nil
}

// decryptArchivePayload 从外层 ZIP 读取 payload.enc 并解密为内部 ZIP。
func decryptArchivePayload(archivePath, payload, target, password string, salt []byte, iterations int, legacy bool) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidArchive, err)
	}
	defer reader.Close()

	for _, entry := range reader.File {
		if entry.FileInfo().IsDir() {
			continue
		}
		name := path.Base(strings.ReplaceAll(entry.Name, "\\", "/"))
		if !strings.EqualFold(name, "payload.enc") || (payload != "" && entry.Name != payload) {
			continue
		}
		file, err := entry.Open()
		if err != nil {
			return ErrInvalidArchive
		}
		decryptErr := decryptStream(file, target, password, salt, iterations, legacy)
		_ = file.Close()
		return decryptErr
	}
	return ErrInvalidArchive
}

// extractArchive 从 ZIP 中提取数据库与配置文件，拒绝路径穿越和超大条目。
func extractArchive(archivePath, workDir string) (string, string, error) {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return "", "", fmt.Errorf("%w: %v", ErrInvalidArchive, err)
	}
	defer reader.Close()

	var dbPath, configPath string
	for _, entry := range reader.File {
		if entry.FileInfo().IsDir() {
			continue
		}
		name := path.Base(strings.ReplaceAll(entry.Name, "\\", "/"))
		if strings.HasPrefix(name, "._") || name == "" {
			continue
		}
		lower := strings.ToLower(name)
		switch {
		case dbPath == "" && (lower == DatabaseName || strings.HasSuffix(lower, ".db")):
			if entry.UncompressedSize64 > uint64(MaxUploadSize) {
				return "", "", ErrTooLarge
			}
			target := filepath.Join(workDir, DatabaseName)
			if err := extractEntry(entry, target, MaxUploadSize); err != nil {
				return "", "", err
			}
			dbPath = target
		case dbPath == "" && (lower == PostgresDatabaseName || strings.HasSuffix(lower, ".dump")):
			if entry.UncompressedSize64 > uint64(MaxUploadSize) {
				return "", "", ErrTooLarge
			}
			target := filepath.Join(workDir, PostgresDatabaseName)
			if err := extractEntry(entry, target, MaxUploadSize); err != nil {
				return "", "", err
			}
			dbPath = target
		case dbPath == "" && (lower == MySQLDatabaseName || strings.HasSuffix(lower, ".sql")):
			if entry.UncompressedSize64 > uint64(MaxUploadSize) {
				return "", "", ErrTooLarge
			}
			target := filepath.Join(workDir, MySQLDatabaseName)
			if err := extractEntry(entry, target, MaxUploadSize); err != nil {
				return "", "", err
			}
			dbPath = target
		case configPath == "" && (lower == ConfigName || strings.HasSuffix(lower, ".yaml") || strings.HasSuffix(lower, ".yml")):
			if entry.UncompressedSize64 > maxConfigSize {
				return "", "", fmt.Errorf("%w: 配置文件过大", ErrInvalidArchive)
			}
			target := filepath.Join(workDir, ConfigName)
			if err := extractEntry(entry, target, maxConfigSize); err != nil {
				return "", "", err
			}
			configPath = target
		}
	}
	if dbPath == "" {
		return "", "", ErrInvalidArchive
	}
	return dbPath, configPath, nil
}

func extractEntry(entry *zip.File, target string, limit int64) error {
	file, err := entry.Open()
	if err != nil {
		return err
	}
	defer file.Close()
	return writeLimitedFile(target, file, limit)
}

// validateDatabase 检查备份文件头和基础表结构，降低恢复错误文件的风险。
func validateDatabase(driver, filePath string) error {
	if driver == "postgres" {
		return validatePostgresDump(filePath)
	}
	if driver == "mysql" {
		return validateMySQLDump(filePath)
	}
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	header := make([]byte, len(sqliteHeader))
	n, err := io.ReadFull(file, header)
	_ = file.Close()
	if err != nil || n != len(sqliteHeader) || string(header) != sqliteHeader {
		return ErrInvalidDatabase
	}

	dsn := "file:" + filepath.ToSlash(filePath) + "?mode=ro&_pragma=busy_timeout(2000)"
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidDatabase, err)
	}
	defer conn.Close()

	var check string
	if err := conn.QueryRow(`PRAGMA quick_check`).Scan(&check); err != nil || check != "ok" {
		return fmt.Errorf("%w: 完整性检查失败", ErrInvalidDatabase)
	}
	for _, table := range requiredTables {
		var name string
		err := conn.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name)
		if err != nil {
			return fmt.Errorf("%w: 缺少数据表 %s", ErrInvalidDatabase, table)
		}
	}
	return nil
}

func validatePostgresDump(filePath string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	header := make([]byte, 5)
	n, err := io.ReadFull(file, header)
	_ = file.Close()
	if err != nil || n != 5 || string(header) != "PGDMP" {
		return ErrInvalidDatabase
	}
	pgRestore, err := findPgRestore()
	if err != nil {
		return err
	}
	output, err := exec.Command(pgRestore, "--list", filePath).CombinedOutput() // #nosec G204 -- pg_restore 从 PATH 解析，文件路径为服务端临时目录
	if err != nil {
		return fmt.Errorf("%w: pg_restore --list 失败: %v", ErrInvalidDatabase, err)
	}
	text := string(output)
	for _, table := range requiredTables {
		if !strings.Contains(text, " "+table+" ") && !strings.HasSuffix(text, " "+table) {
			return fmt.Errorf("%w: 缺少数据表 %s", ErrInvalidDatabase, table)
		}
	}
	return nil
}

// validateMySQLDump 校验逻辑备份包含基础表定义。
func validateMySQLDump(filePath string) error {
	raw, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		return ErrInvalidDatabase
	}
	text := string(raw[:min(len(raw), 512)])
	upper := strings.ToUpper(text)
	if !strings.Contains(upper, "MYSQL DUMP") && !strings.Contains(upper, "MARIADB DUMP") &&
		!strings.Contains(text, "/*!40101") && !strings.Contains(text, "CREATE TABLE") {
		return ErrInvalidDatabase
	}
	full := string(raw)
	for _, table := range requiredTables {
		if !strings.Contains(full, "CREATE TABLE `"+table+"`") &&
			!strings.Contains(full, "CREATE TABLE "+table) {
			return fmt.Errorf("%w: 缺少数据表 %s", ErrInvalidDatabase, table)
		}
	}
	return nil
}

func looksLikeMySQLDump(filePath string) bool {
	file, err := os.Open(filePath)
	if err != nil {
		return false
	}
	defer file.Close()
	header := make([]byte, 512)
	n, _ := file.Read(header)
	text := strings.ToUpper(string(header[:n]))
	return strings.Contains(text, "MYSQL DUMP") || strings.Contains(text, "MARIADB DUMP")
}

func findPgRestore() (string, error) {
	if path, err := exec.LookPath("pg_restore"); err == nil {
		return path, nil
	}
	for _, candidate := range []string{"/usr/pgsql-18/bin/pg_restore", "/usr/bin/pg_restore", "/usr/local/bin/pg_restore"} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", errors.New("未找到 pg_restore；请安装 PostgreSQL 18 客户端")
}

// verifyBackupKeyPair 用配置文件中的 security.salt 试解数据库里的密文字段，
// 避免恢复后 TOTP、凭据、证书/CA 私钥全部无法解密却提示成功。
func verifyBackupKeyPair(dbPath, configPath string) error {
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	var cfg struct {
		Security struct {
			Salt string `yaml:"salt"`
		} `yaml:"security"`
	}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return ErrInvalidConfig
	}
	salt := strings.TrimSpace(cfg.Security.Salt)
	if salt == "" {
		return ErrKeyMismatch
	}
	cipher, err := util.NewCipher([]byte(salt))
	if err != nil {
		return err
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	checks := []string{
		`SELECT totp_secret FROM users WHERE totp_secret LIKE 'enc:v1:%' LIMIT 1`,
		`SELECT key_pem FROM certificates WHERE key_pem LIKE 'enc:v1:%' LIMIT 1`,
		`SELECT key_pem FROM private_cas WHERE key_pem LIKE 'enc:v1:%' LIMIT 1`,
		`SELECT config FROM dns_accounts WHERE config LIKE 'enc:v1:%' LIMIT 1`,
		`SELECT config FROM deploy_accounts WHERE config LIKE 'enc:v1:%' LIMIT 1`,
		`SELECT eab_hmac FROM acme_cas WHERE eab_hmac LIKE 'enc:v1:%' LIMIT 1`,
	}
	for _, query := range checks {
		var value string
		if err := db.QueryRow(query).Scan(&value); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return err
		}
		if _, err := cipher.Decrypt(value); err != nil {
			return ErrKeyMismatch
		}
	}
	return nil
}

// validateConfig 确认恢复的配置可以被安全加载。
// 除了格式与必填密钥外，还会校验监听地址、门禁入口、TLS/mTLS 组合等，
// 避免非法配置写入 restore-pending 后导致下次启动反复失败。
func validateConfig(filePath string) error {
	if err := config.ValidateFile(filePath); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	// 恢复包必须携带数据库加密盐，否则新生成的盐会让旧数据库中的密文无法解密。
	raw, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}
	var doc struct {
		Security struct {
			Salt string `yaml:"salt"`
		} `yaml:"security"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	if strings.TrimSpace(doc.Security.Salt) == "" {
		return fmt.Errorf("%w: 缺少 security.salt", ErrInvalidConfig)
	}
	return nil
}

// PendingDir 返回待恢复文件目录。
func PendingDir(dataDir string) string {
	return filepath.Join(dataDir, pendingDirName)
}

// CancelPending 删除尚未应用的恢复内容。
// 首次初始化成功后调用，避免此前匿名上传的 pending 恢复包在后续重启时覆盖新账户。
func (m *Manager) CancelPending() error {
	if m == nil || m.DataDir == "" {
		return nil
	}
	return os.RemoveAll(PendingDir(m.DataDir))
}

// Pending 描述一组等待服务重启后应用的恢复文件。
type Pending struct {
	Root         string
	Driver       string
	DatabasePath string
	ConfigPath   string
}

// LoadPending 读取待恢复目录；没有待恢复内容时返回 nil。
func LoadPending(dataDir string) (*Pending, error) {
	root := PendingDir(dataDir)
	pending := &Pending{Root: root, Driver: "sqlite"}
	if data, err := os.ReadFile(filepath.Join(root, driverName)); err == nil {
		switch strings.TrimSpace(string(data)) {
		case "postgres":
			pending.Driver = "postgres"
		case "mysql":
			pending.Driver = "mysql"
		}
	}
	if stat, err := os.Stat(filepath.Join(root, DatabaseName)); err == nil && stat.Mode().IsRegular() {
		pending.DatabasePath = filepath.Join(root, DatabaseName)
	}
	if stat, err := os.Stat(filepath.Join(root, PostgresDatabaseName)); err == nil && stat.Mode().IsRegular() {
		pending.DatabasePath = filepath.Join(root, PostgresDatabaseName)
		pending.Driver = "postgres"
	}
	if stat, err := os.Stat(filepath.Join(root, MySQLDatabaseName)); err == nil && stat.Mode().IsRegular() {
		pending.DatabasePath = filepath.Join(root, MySQLDatabaseName)
		pending.Driver = "mysql"
	}
	if stat, err := os.Stat(filepath.Join(root, ConfigName)); err == nil && stat.Mode().IsRegular() {
		pending.ConfigPath = filepath.Join(root, ConfigName)
	}
	if pending.DatabasePath == "" && pending.ConfigPath == "" {
		return nil, nil
	}
	return pending, nil
}

// Cleanup 删除已应用的待恢复文件。
func (p *Pending) Cleanup() error {
	if p == nil || p.Root == "" {
		return nil
	}
	return os.RemoveAll(p.Root)
}

// Quarantine 把无法应用的恢复包移到带时间戳的旁路目录，
// 避免每次启动都重复尝试同一份坏配置而无法启动。
func (p *Pending) Quarantine() error {
	if p == nil || p.Root == "" {
		return nil
	}
	target := p.Root + ".failed-" + time.Now().UTC().Format("20060102-150405")
	return os.Rename(p.Root, target)
}

// ApplyDatabase 用恢复文件替换目标数据库，并清理旧的 WAL/SHM 文件。
func ApplyDatabase(target, source string) error {
	if target == "" || source == "" {
		return errors.New("数据库路径未配置")
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	suffix := fmt.Sprintf("%d", time.Now().UTC().UnixNano())
	temp := target + ".restore-" + suffix
	if err := copyFile(source, temp); err != nil {
		_ = os.Remove(temp)
		return err
	}
	backup := target + ".before-restore-" + suffix
	hadOld := false
	if _, err := os.Stat(target); err == nil {
		if err := copyFile(target, backup); err != nil {
			_ = os.Remove(temp)
			return fmt.Errorf("备份现有数据库失败: %w", err)
		}
		hadOld = true
	}
	// 旧 WAL/SHM 属于被替换的数据库；改名而不是删除，回滚时可以一并恢复。
	sidecars := []string{"-wal", "-shm"}
	type sidecarBackup struct {
		suffix string
		backup string
	}
	movedSidecars := make([]sidecarBackup, 0, len(sidecars))
	rollbackSidecars := func() {
		for _, item := range movedSidecars {
			_ = os.Rename(item.backup, target+item.suffix)
		}
	}
	for _, suffixName := range sidecars {
		sourceSidecar := target + suffixName
		if _, err := os.Stat(sourceSidecar); err != nil {
			continue
		}
		backupSidecar := backup + suffixName
		if err := os.Rename(sourceSidecar, backupSidecar); err != nil {
			rollbackSidecars()
			_ = os.Remove(temp)
			if hadOld {
				_ = os.Remove(backup)
			}
			return fmt.Errorf("备份数据库 WAL 失败: %w", err)
		}
		movedSidecars = append(movedSidecars, sidecarBackup{suffix: suffixName, backup: backupSidecar})
	}
	rollback := func() {
		if hadOld {
			_ = os.Remove(target)
			if err := os.Rename(backup, target); err != nil {
				_ = copyFile(backup, target)
			}
		} else {
			_ = os.Remove(target)
		}
		rollbackSidecars()
		_ = os.Remove(temp)
	}
	// 同文件系统优先原子 rename；绑定挂载单文件无法 rename 时退化为原地写入。
	if err := os.Rename(temp, target); err != nil {
		if writeErr := copyFile(source, target); writeErr != nil {
			rollback()
			return fmt.Errorf("替换数据库失败: %w", err)
		}
		_ = os.Remove(temp)
	}
	_ = os.Remove(target + "-wal")
	_ = os.Remove(target + "-shm")
	for _, item := range movedSidecars {
		_ = os.Remove(item.backup)
	}
	if hadOld {
		_ = os.Remove(backup)
	}
	return nil
}

// ApplyPostgresDatabase 用 pg_restore 恢复 PostgreSQL 备份。
// 调用方必须在数据库连接打开前调用（服务重启的启动阶段）。
func ApplyPostgresDatabase(dsn, source string) error {
	if strings.TrimSpace(dsn) == "" || strings.TrimSpace(source) == "" {
		return errors.New("PostgreSQL DSN 或备份文件未配置")
	}
	pgDump, err := findPgDump()
	if err != nil {
		return err
	}
	pgRestore, err := findPgRestore()
	if err != nil {
		return err
	}
	suffix := fmt.Sprintf("%d", time.Now().UTC().UnixNano())
	rollback := source + ".before-restore-" + suffix
	safeDSN, pgEnv := db.PostgresCLIEnv(dsn)
	dumpCmd := exec.Command(pgDump, "--format=custom", "--no-owner", "--no-privileges", "--file", rollback, safeDSN) // #nosec G204 -- pg_dump 从 PATH 解析，参数由服务端生成
	dumpCmd.Env = append(os.Environ(), pgEnv...)
	if output, err := dumpCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("恢复前备份当前 PostgreSQL 失败: %w: %s", err, strings.TrimSpace(string(output)))
	}
	defer os.Remove(rollback)
	restoreCmd := exec.Command(pgRestore, "--clean", "--if-exists", "--no-owner", "--no-privileges", "--dbname", safeDSN, source) // #nosec G204 -- pg_restore 从 PATH 解析，参数由服务端生成
	restoreCmd.Env = append(os.Environ(), pgEnv...)
	if output, err := restoreCmd.CombinedOutput(); err != nil {
		// 尝试把恢复前的备份写回，降低半恢复状态的影响。
		rollbackCmd := exec.Command(pgRestore, "--clean", "--if-exists", "--no-owner", "--no-privileges", "--dbname", safeDSN, rollback) // #nosec G204 -- pg_restore 从 PATH 解析，参数由服务端生成
		rollbackCmd.Env = append(os.Environ(), pgEnv...)
		_, _ = rollbackCmd.CombinedOutput()
		return fmt.Errorf("pg_restore 失败: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

// ApplyMySQLDatabase 用 mysql 客户端恢复 MySQL 逻辑备份。
// 调用方必须在数据库连接打开前调用（服务重启的启动阶段）。
func ApplyMySQLDatabase(dsn, source string) error {
	if strings.TrimSpace(dsn) == "" || strings.TrimSpace(source) == "" {
		return errors.New("MySQL DSN 或备份文件未配置")
	}
	mysqlDump, err := findMySQLDump()
	if err != nil {
		return err
	}
	mysqlClient, err := findMySQLClient()
	if err != nil {
		return err
	}
	connection, err := db.MySQLCLI(dsn)
	if err != nil {
		return err
	}
	if strings.TrimSpace(connection.Database) == "" {
		return errors.New("MySQL DSN 必须包含数据库名")
	}
	suffix := fmt.Sprintf("%d", time.Now().UTC().UnixNano())
	rollback := source + ".before-restore-" + suffix
	dumpCmd := exec.Command(mysqlDump, db.MySQLDumpArgs(mysqlDump, connection, rollback)...) // #nosec G204 -- mysqldump 从 PATH 解析，参数由服务端生成
	dumpCmd.Env = append(os.Environ(), "MYSQL_PWD="+connection.Password)
	if output, err := dumpCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("恢复前备份当前 MySQL 失败: %w: %s", err, strings.TrimSpace(string(output)))
	}
	defer os.Remove(rollback)
	restoreFile, err := os.Open(source)
	if err != nil {
		return err
	}
	defer restoreFile.Close()
	restoreCmd := exec.Command(mysqlClient, db.MySQLRestoreArgs(mysqlClient, connection)...) // #nosec G204 -- mysql 从 PATH 解析，参数由服务端生成
	restoreCmd.Env = append(os.Environ(), "MYSQL_PWD="+connection.Password)
	restoreCmd.Stdin = restoreFile
	if output, err := restoreCmd.CombinedOutput(); err != nil {
		// 尝试把恢复前的备份写回，降低半恢复状态的影响。
		if rollbackFile, openErr := os.Open(rollback); openErr == nil {
			rollbackCmd := exec.Command(mysqlClient, db.MySQLRestoreArgs(mysqlClient, connection)...) // #nosec G204 -- mysql 从 PATH 解析，参数由服务端生成
			rollbackCmd.Env = append(os.Environ(), "MYSQL_PWD="+connection.Password)
			rollbackCmd.Stdin = rollbackFile
			_, _ = rollbackCmd.CombinedOutput()
			_ = rollbackFile.Close()
		}
		return fmt.Errorf("mysql 恢复失败: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

// ApplyConfig 用恢复文件替换配置文件，权限保持 0600。
func ApplyConfig(target, source string) error {
	if target == "" || source == "" {
		return errors.New("配置文件路径未配置")
	}
	dir := filepath.Dir(target)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	temp := target + ".restore"
	if err := copyFile(source, temp); err != nil {
		_ = os.Remove(temp)
		return err
	}
	if err := os.Rename(temp, target); err != nil {
		// 目标可能是绑定挂载的单文件，无法 rename 覆盖，退化为原地写入。
		if copyErr := copyFile(source, target); copyErr != nil {
			_ = os.Remove(temp)
			return fmt.Errorf("替换配置文件失败: %w", err)
		}
		_ = os.Remove(temp)
	}
	return nil
}

func writeLimitedFile(target string, source io.Reader, limit int64) error {
	file, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(file, io.LimitReader(source, limit+1))
	syncErr := file.Sync()
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	if written > limit {
		return ErrTooLarge
	}
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

func copyFile(source, target string) error {
	file, err := os.Open(source)
	if err != nil {
		return err
	}
	defer file.Close()
	return writeLimitedFile(target, file, MaxUploadSize)
}

func findPgDump() (string, error) {
	if path, err := exec.LookPath("pg_dump"); err == nil {
		return path, nil
	}
	for _, candidate := range []string{"/usr/pgsql-18/bin/pg_dump", "/usr/bin/pg_dump", "/usr/local/bin/pg_dump"} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", errors.New("未找到 pg_dump；请安装 PostgreSQL 18 客户端")
}

func findMySQLDump() (string, error) {
	if path, err := exec.LookPath("mysqldump"); err == nil {
		return path, nil
	}
	for _, candidate := range []string{"/usr/bin/mysqldump", "/usr/local/bin/mysqldump", "/usr/local/mysql/bin/mysqldump"} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", errors.New("未找到 mysqldump；请安装 MySQL 客户端")
}

func findMySQLClient() (string, error) {
	if path, err := exec.LookPath("mysql"); err == nil {
		return path, nil
	}
	for _, candidate := range []string{"/usr/bin/mysql", "/usr/local/bin/mysql", "/usr/local/mysql/bin/mysql"} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", errors.New("未找到 mysql 客户端；无法恢复 MySQL 备份")
}
