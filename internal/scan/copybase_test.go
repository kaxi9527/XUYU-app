package scan

import "testing"

func TestStripVersion(t *testing.T) {
	cases := map[string]string{
		"login-handoff-r4":            "login-handoff",
		"login-handoff-20261101-2315": "login-handoff",
		"login-handoff-20261101":      "login-handoff",
		"login-handoff-a1b2c3d4e5f6":  "login-handoff",
		"报告 副本":                       "报告",
		"报告 副本 (2)":                   "报告",
		"报告（1）":                       "报告",
		"site copy":                   "site",
		"site-copy2":                  "site",
		"site-2":                      "site",
		"site 2":                      "site",
		"site-v3":                     "site",
		"site-1.2.0":                  "site",
		"site.bak-old":                "site",
		"site-final":                  "site",
		"海报-最终版2":                     "海报",
		"python3":                     "python3",
		"deadbeefcafe":                "deadbeefcafe",
		"my-app":                      "my-app",
		"backup":                      "backup",
		"2026":                        "2026",
	}
	for in, want := range cases {
		if got := StripVersion(in); got != want {
			t.Errorf("StripVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestKeys(t *testing.T) {
	if CopyKey("Login-Handoff-r3.zip") != "login-handoff" {
		t.Fatal(CopyKey("Login-Handoff-r3.zip"))
	}
	if FileKey("Login-Handoff-r3.tar.gz") != "login-handoff" {
		t.Fatal(FileKey("Login-Handoff-r3.tar.gz"))
	}
	if FileKey("notes.md") != "notes" {
		t.Fatal(FileKey("notes.md"))
	}
}
