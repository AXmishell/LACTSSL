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
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
	_ "modernc.org/sqlite"
)

const (
	// DatabaseName 是备份包内数据库文件的固定名称。
	DatabaseName = "lactssl.db"
	// ConfigName 是备份包内配置文件的固定名称。
	ConfigName = "config.yaml"
	// ManifestName 是备份包内元数据文件的固定名称。
	ManifestName = "manifest.json"
	// MaxUploadSize 限制恢复上传的大小，避免占用过多磁盘和内存。
	MaxUploadSize int64 = 256 << 20

	// 加密备份使用 PBKDF2-HMAC-SHA256 派生密钥，并以分块 AES-256-GCM 加密内部 ZIP。
	encryptionKDF        = "pbkdf2-sha256"
	encryptionCipher     = "aes-256-gcm"
	encryptionIterations = 600000
	encryptionChunkSize  = 1 << 20
	encryptionSaltSize   = 16
	encryptionAAD        = "LACTSSL-BACKUP-V1"

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
	DB         *sql.DB
	DataDir    string
	DBPath     string
	ConfigFile string
	Version    string

	mu sync.Mutex
}

// New 创建备份管理器。
func New(db *sql.DB, dataDir, dbPath, configFile, version string) *Manager {
	return &Manager{
		DB:         db,
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
	if stat, err := os.Stat(m.DBPath); err == nil {
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

	dbSnapshot := filepath.Join(tempDir, DatabaseName)
	if err := m.snapshot(dbSnapshot); err != nil {
		cleanup()
		return "", "", func() {}, err
	}

	plainPath := filepath.Join(tempDir, "backup.zip")
	meta := manifest{
		App:       "LACTSSL",
		Version:   m.Version,
		CreatedAt: time.Now().UTC(),
		Database:  DatabaseName,
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
	if err := addFileToZip(zw, DatabaseName, dbSnapshot); err != nil {
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
		CreatedAt:  inner.CreatedAt,
		Encrypted:  true,
		KDF:        encryptionKDF,
		Iterations: encryptionIterations,
		Salt:       base64.StdEncoding.EncodeToString(salt),
		Cipher:     encryptionCipher,
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
	aad := []byte(encryptionAAD)
	for {
		n, readErr := io.ReadFull(in, buffer)
		if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
			return readErr
		}
		if n > 0 {
			nonce := make([]byte, gcm.NonceSize())
			if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
				return err
			}
			ciphertext := gcm.Seal(nil, nonce, buffer[:n], aad)
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
func decryptStream(source io.Reader, target, password string, salt []byte, iterations int) error {
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
	for {
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
		plain, err := gcm.Open(nil, nonce, ciphertext, aad)
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
	)
	reader, err := os.Open(uploadPath)
	if err != nil {
		return err
	}
	buffered := bufio.NewReader(reader)
	magic, _ := buffered.Peek(4)
	isZip := bytes.Equal(magic, []byte("PK\x03\x04"))
	_ = reader.Close()

	if isZip {
		meta, payload, err := readArchiveManifest(uploadPath)
		if err != nil {
			return err
		}
		if meta != nil && meta.Encrypted {
			if password == "" {
				return ErrPasswordRequired
			}
			if (meta.Cipher != "" && meta.Cipher != encryptionCipher) || (meta.KDF != "" && meta.KDF != encryptionKDF) {
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
			if iterations < 10000 || iterations > 2000000 {
				return ErrInvalidArchive
			}
			innerPath := filepath.Join(workDir, "payload.zip")
			if err := decryptArchivePayload(uploadPath, payload, innerPath, password, salt, iterations); err != nil {
				return err
			}
			dbPath, configPath, err = extractArchive(innerPath, workDir)
		} else {
			dbPath, configPath, err = extractArchive(uploadPath, workDir)
		}
	} else {
		dbPath = uploadPath
	}
	if err != nil {
		return err
	}
	if dbPath == "" {
		return ErrInvalidArchive
	}
	if err := validateDatabase(dbPath); err != nil {
		return err
	}
	if configPath != "" {
		if err := validateConfig(configPath); err != nil {
			return err
		}
	}

	pending := PendingDir(m.DataDir)
	next := pending + ".new"
	_ = os.RemoveAll(next)
	if err := os.MkdirAll(next, 0o700); err != nil {
		return fmt.Errorf("创建待恢复目录失败: %w", err)
	}
	if err := copyFile(dbPath, filepath.Join(next, DatabaseName)); err != nil {
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
func decryptArchivePayload(archivePath, payload, target, password string, salt []byte, iterations int) error {
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
		decryptErr := decryptStream(file, target, password, salt, iterations)
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

// validateDatabase 检查 SQLite 文件头和基础表结构，降低恢复错误文件的风险。
func validateDatabase(filePath string) error {
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

// validateConfig 确认配置文件是可解析的 YAML，且包含安全配置段。
func validateConfig(filePath string) error {
	raw, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	security, ok := doc["security"].(map[string]any)
	if !ok {
		return fmt.Errorf("%w: 缺少 security 配置段", ErrInvalidConfig)
	}
	if _, ok := security["salt"]; !ok {
		return fmt.Errorf("%w: 缺少 security.salt", ErrInvalidConfig)
	}
	return nil
}

// PendingDir 返回待恢复文件目录。
func PendingDir(dataDir string) string {
	return filepath.Join(dataDir, pendingDirName)
}

// Pending 描述一组等待服务重启后应用的恢复文件。
type Pending struct {
	Root         string
	DatabasePath string
	ConfigPath   string
}

// LoadPending 读取待恢复目录；没有待恢复内容时返回 nil。
func LoadPending(dataDir string) (*Pending, error) {
	root := PendingDir(dataDir)
	pending := &Pending{Root: root}
	if stat, err := os.Stat(filepath.Join(root, DatabaseName)); err == nil && stat.Mode().IsRegular() {
		pending.DatabasePath = filepath.Join(root, DatabaseName)
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

// ApplyDatabase 用恢复文件替换目标数据库，并清理旧的 WAL/SHM 文件。
func ApplyDatabase(target, source string) error {
	if target == "" || source == "" {
		return errors.New("数据库路径未配置")
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	temp := target + ".restore"
	if err := copyFile(source, temp); err != nil {
		_ = os.Remove(temp)
		return err
	}
	// 旧 WAL/SHM 属于被替换的数据库，必须一并清理。
	_ = os.Remove(target + "-wal")
	_ = os.Remove(target + "-shm")
	if err := os.Rename(temp, target); err != nil {
		// 目标可能是绑定挂载的单文件，无法 rename 覆盖，退化为原地写入。
		if copyErr := copyFile(source, target); copyErr != nil {
			_ = os.Remove(temp)
			return fmt.Errorf("替换数据库失败: %w", err)
		}
		_ = os.Remove(temp)
	}
	_ = os.Remove(target + "-wal")
	_ = os.Remove(target + "-shm")
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
