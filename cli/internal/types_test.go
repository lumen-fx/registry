package internal

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestUserPublicDropsPrivateFields(t *testing.T) {
	u := User{
		ID:           uuid.New(),
		Username:     "ada",
		Email:        "ada@example.com",
		PasswordHash: "argon2id$...",
		CreatedAt:    time.Now(),
		Packages:     []Package{{Name: "lantern"}},
	}

	p := u.Public()
	if p.ID != u.ID || p.Username != u.Username || !p.CreatedAt.Equal(u.CreatedAt) {
		t.Errorf("Public() lost a public field: %+v vs %+v", p, u)
	}
	if len(p.Packages) != 1 || p.Packages[0].Name != "lantern" {
		t.Errorf("Public() lost packages: %+v", p.Packages)
	}

	out, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{u.Email, u.PasswordHash} {
		if strings.Contains(string(out), secret) {
			t.Errorf("public JSON leaks %q: %s", secret, out)
		}
	}
}

func TestUserJSONHidesPasswordHash(t *testing.T) {
	u := User{Username: "ada", PasswordHash: "argon2id$..."}
	out, err := json.Marshal(u)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), u.PasswordHash) {
		t.Errorf("user JSON leaks the password hash: %s", out)
	}
}
