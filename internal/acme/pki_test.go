package acme

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"net/http"
	"strconv"
	"testing"
	"time"

	"golang.org/x/crypto/ocsp"
	"lactssl/internal/model"
	"lactssl/internal/service"
)

func issueTestCertificate(t *testing.T, ca *model.PrivateCA) (*model.Certificate, *x509.Certificate) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject:  pkix.Name{CommonName: "localhost"},
		DNSNames: []string{"localhost"},
	}, key)
	if err != nil {
		t.Fatal(err)
	}
	csr, _ := x509.ParseCertificateRequest(csrDER)
	issued, err := service.SignCSR(ca, csr, []service.CertificateIdentifier{{Type: "dns", Value: "localhost"}}, service.SignCSROptions{
		NotBefore: time.Now().UTC().Add(-time.Minute),
		NotAfter:  time.Now().UTC().AddDate(0, 0, 90),
	})
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode([]byte(issued.CertPEM))
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	certificate := &model.Certificate{
		Domains:       []string{"localhost"},
		PrimaryDomain: "localhost",
		Issuer:        issued.Issuer,
		CA:            "private",
		Source:        "acme",
		PrivateCAID:   ca.ID,
		CertPEM:       issued.CertPEM,
		FullchainPEM:  issued.FullchainPEM,
		Serial:        issued.Serial,
		NotBefore:     issued.NotBefore,
		NotAfter:      issued.NotAfter,
		Status:        "valid",
	}
	return certificate, leaf
}

func TestCRLAndOCSPRevocation(t *testing.T) {
	_, st, _, ts, ca := newOrderTestServer(t)
	certificate, leaf := issueTestCertificate(t, ca)
	if err := st.CreateCertificate(certificate); err != nil {
		t.Fatal(err)
	}
	caBlock, _ := pem.Decode([]byte(ca.CertPEM))
	caCert, _ := x509.ParseCertificate(caBlock.Bytes)

	// 未撤销时 OCSP 应为 Good。
	requestBytes, err := ocsp.CreateRequest(leaf, caCert, nil)
	if err != nil {
		t.Fatal(err)
	}
	postOCSP := func() []byte {
		url := ts.URL + "/pki/" + strconv.FormatInt(ca.ID, 10) + "/ocsp"
		resp, err := http.Post(url, "application/ocsp-request", bytes.NewReader(requestBytes))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		return data
	}
	response, err := ocsp.ParseResponseForCert(postOCSP(), leaf, caCert)
	if err != nil {
		t.Fatal(err)
	}
	if response.Status != ocsp.Good {
		t.Fatalf("未撤销证书的 OCSP 状态应为 Good: %d", response.Status)
	}

	// 撤销后 OCSP 应为 Revoked，CRL 应包含序列号。
	if err := st.RevokeCertificate(certificate.ID, 1, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	response, err = ocsp.ParseResponseForCert(postOCSP(), leaf, caCert)
	if err != nil {
		t.Fatal(err)
	}
	if response.Status != ocsp.Revoked {
		t.Fatalf("已撤销证书的 OCSP 状态应为 Revoked: %d", response.Status)
	}

	crlResp, err := http.Get(ts.URL + "/pki/" + strconv.FormatInt(ca.ID, 10) + "/crl")
	if err != nil {
		t.Fatal(err)
	}
	defer crlResp.Body.Close()
	if crlResp.StatusCode != http.StatusOK {
		t.Fatalf("CRL 请求失败: %d", crlResp.StatusCode)
	}
	crlDER, _ := io.ReadAll(crlResp.Body)
	crl, err := x509.ParseRevocationList(crlDER)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range crl.RevokedCertificateEntries {
		if entry.SerialNumber.Cmp(leaf.SerialNumber) == 0 {
			found = true
		}
	}
	if !found {
		t.Fatal("CRL 未包含已撤销证书序列号")
	}

	// 内容未变化时重复请求不应继续膨胀 CRL Number。
	againResp, err := http.Get(ts.URL + "/pki/" + strconv.FormatInt(ca.ID, 10) + "/crl")
	if err != nil {
		t.Fatal(err)
	}
	defer againResp.Body.Close()
	againDER, _ := io.ReadAll(againResp.Body)
	again, err := x509.ParseRevocationList(againDER)
	if err != nil {
		t.Fatal(err)
	}
	if crl.Number.Cmp(again.Number) != 0 {
		t.Fatalf("吊销集合未变化时 CRL Number 不应递增: %s != %s", crl.Number, again.Number)
	}
	if err := crl.CheckSignatureFrom(caCert); err != nil {
		t.Fatalf("CRL 签名校验失败: %v", err)
	}
}
