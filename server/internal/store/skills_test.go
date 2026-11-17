package store_test

import (
	"testing"

	"github.com/andrewsl105/fleetpocket/server/internal/store"
	"github.com/andrewsl105/fleetpocket/server/internal/testutil"
)

func TestSeedSkillsOnce(t *testing.T) {
	st := testutil.DB(t)
	ctx := t.Context()
	seed := []store.Skill{{Slug: "db-schema", Name: "db-schema", Description: "d", Body: "b"}}
	if err := st.SeedSkills(ctx, seed); err != nil {
		t.Fatal(err)
	}
	list, _ := st.ListSkills(ctx)
	if len(list) != 1 || list[0].EnabledByDefault {
		t.Fatalf("%+v", list)
	}
	// A deleted seed skill stays deleted.
	if err := st.DeleteSkill(ctx, list[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := st.SeedSkills(ctx, seed); err != nil {
		t.Fatal(err)
	}
	if list, _ := st.ListSkills(ctx); len(list) != 0 {
		t.Fatalf("reseeded: %+v", list)
	}
}
