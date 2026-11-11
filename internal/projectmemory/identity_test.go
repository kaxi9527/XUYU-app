package projectmemory

import "testing"

func TestRenameWithPathReuseAndSwapKeepsObjectIdentity(t *testing.T) {
	for _, swap := range []bool{false, true} {
		s, scope := memoryFixture(t)
		initialize(t, s, scope)
		a, b := fact(scope, "a", "object-a", "a"), fact(scope, "b", "object-b", "b")
		observe(t, s, observation(scope, 1, a, b), "initial")
		before := readMemory(t, s)
		ids := map[string]string{}
		for _, f := range before.State.Files {
			ids[f.Fact.ObjectKey] = f.ID
		}
		if swap {
			a.Path, b.Path = b.Path, a.Path
		} else {
			a.Path = fact(scope, "c", "", "").Path
		}
		files := []Fact{a, b}
		if !swap {
			files = append(files, fact(scope, "a", "replacement", "new"))
		}
		r := observe(t, s, observation(scope, 2, files...), "rename")
		for _, e := range r.Record.Events {
			if e.Kind == "no_longer_seen" {
				t.Fatalf("observed object lost during rename: %+v", e)
			}
		}
		for _, f := range readMemory(t, s).State.Files {
			if id := ids[f.Fact.ObjectKey]; id != "" && (id != f.ID || f.Status != "present") {
				t.Fatalf("identity changed: %+v", f)
			}
		}
	}
}
