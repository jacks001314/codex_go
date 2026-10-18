//go:build darwin

package appserverdaemon

import (
	"os"
	"testing"
)

func TestDarwinNativePIDIdentityIncludesUniqueIDAndBootID(t *testing.T) {
	identity, err := readNativePIDProcessIdentity(uint32(os.Getpid()))
	if err != nil {
		t.Fatal(err)
	}
	if identity == nil || identity.UniqueID == nil || identity.BootID == "" ||
		identity.StartSeconds == nil || identity.StartMicroseconds == nil {
		t.Fatalf("identity = %#v, want boot/unique/start identity", identity)
	}
	matched, zombie, available, err := matchNativePIDProcessIdentity(identity, uint32(os.Getpid()))
	if err != nil || !available || !matched || zombie {
		t.Fatalf("match = %v/%v/%v, err=%v", matched, zombie, available, err)
	}
}
