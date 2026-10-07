package guardauth

import (
	"strings"
	"testing"
)

func TestHashParams(t *testing.T) {
	if p, err := (Options{}).hashParams(); p != nil || err != nil {
		t.Errorf("zero options = %v, %v; want Guard defaults", p, err)
	}
	p, err := Options{HashMemoryKiB: 19456, HashTime: 2, HashThreads: 1}.hashParams()
	if err != nil || p.Memory != 19456 || p.KeyLen != 32 || p.SaltLen != 16 {
		t.Errorf("floor = %+v, %v", p, err)
	}
	for _, o := range []Options{
		{HashMemoryKiB: 19455, HashTime: 2, HashThreads: 1},
		{HashMemoryKiB: 65536, HashTime: 1, HashThreads: 2},
		{HashMemoryKiB: 65536, HashTime: 3},
	} {
		if _, err := o.hashParams(); err == nil || !strings.Contains(err.Error(), "PASSWORD_HASH_") {
			t.Errorf("%+v: err = %v", o, err)
		}
	}
}

func TestAuditEmailKey(t *testing.T) {
	if k, err := (Options{}).auditEmailKey(); k != nil || err != nil {
		t.Errorf("empty = %v, %v", k, err)
	}
	k, err := Options{AuditEmailKey: strings.Repeat("ab", 32)}.auditEmailKey()
	if err != nil || len(k) != 32 {
		t.Errorf("32 bytes = %d, %v", len(k), err)
	}
	for _, bad := range []string{"zz", strings.Repeat("ab", 31)} {
		if _, err := (Options{AuditEmailKey: bad}).auditEmailKey(); err == nil || !strings.Contains(err.Error(), "AUDIT_EMAIL_KEY") {
			t.Errorf("%q: err = %v", bad, err)
		}
	}
}
