//go:build windows

package network

import (
	"encoding/pem"
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

func loadProxyPlatformRootBundle() (string, error) {
	name, err := windows.UTF16PtrFromString("ROOT")
	if err != nil {
		return "", err
	}
	store, err := windows.CertOpenSystemStore(0, name)
	if err != nil {
		return "", err
	}
	defer windows.CertCloseStore(store, 0) //nolint:errcheck
	var builder strings.Builder
	var previous *windows.CertContext
	for {
		certificate, enumErr := windows.CertEnumCertificatesInStore(store, previous)
		if certificate == nil {
			break
		}
		previous = certificate
		if certificate.EncodedCert == nil || certificate.Length == 0 {
			continue
		}
		der := append([]byte(nil), unsafe.Slice(certificate.EncodedCert, int(certificate.Length))...)
		if err := pem.Encode(&builder, &pem.Block{Type: "CERTIFICATE", Bytes: der}); err != nil {
			return "", fmt.Errorf("encode Windows root certificate: %w", err)
		}
		_ = enumErr
	}
	return builder.String(), nil
}
