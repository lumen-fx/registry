package internal

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestUserPublicDropsPrivateFields(t *testing.T) {
	u := User{
		ID:        uuid.New(),
		Username:  "ada",
		CreatedAt: time.Now(),
		Packages:  []Package{{Name: "lantern"}},
	}

	p := u.Public()
	if p.ID != u.ID || p.Username != u.Username || !p.CreatedAt.Equal(u.CreatedAt) {
		t.Errorf("Public() lost a public field: %+v vs %+v", p, u)
	}
	if len(p.Packages) != 1 || p.Packages[0].Name != "lantern" {
		t.Errorf("Public() lost packages: %+v", p.Packages)
	}

	if _, err := json.Marshal(p); err != nil {
		t.Fatal(err)
	}
}
