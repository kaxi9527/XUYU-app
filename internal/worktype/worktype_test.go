package worktype

import (
	"strings"
	"testing"
)

func TestTable(t *testing.T) {
	if n := len(All()); n != 88 {
		t.Fatalf("want 88 rows, got %d", n)
	}
	if n := len(Industries()); n != 23 {
		t.Fatalf("want 23 industries, got %d", n)
	}
	for _, w := range All() {
		if !ValidTemplate(w.Template) {
			t.Fatalf("bad template %+v", w)
		}
	}
}

func TestResolve(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"电商·主图", "电商·主图"},
		{"电商/主图", "电商·主图"},
		{"主图", "电商·主图"},
		{"医疗·病历文书·模板", "医疗·病历文书·模板"},
	} {
		w, err := Resolve(c.in)
		if err != nil || w.Label() != c.want {
			t.Fatalf("%s: %v %v", c.in, w.Label(), err)
		}
	}
	if _, err := Resolve("客服互动"); err == nil || !strings.Contains(err.Error(), "好几个") {
		t.Fatalf("ambiguous merged name should fail: %v", err)
	}
	if _, err := Resolve("不存在的工种"); err == nil {
		t.Fatal("unknown should fail")
	}
	if KindOf("T01") != "图片组" || KindOf("t09") != "代码版本" {
		t.Fatal("kind")
	}
}
