package scan

import (
	"path/filepath"
	"regexp"
	"strings"
)

// 副本/版本后缀。每条都只匹配名字末尾。顺序有讲究：先去扩展名再按轮去后缀。
var suffixRules = []*regexp.Regexp{
	regexp.MustCompile(`\s*[（(]\s*\d+\s*[)）]$`),                                            // foo (2)、foo（1）
	regexp.MustCompile(`\s*(的副本|副本|拷贝|复制|最终版|最终|终版)\s*\d*$`),                               // foo 副本、海报最终版2
	regexp.MustCompile(`(?i)[\s._-]+(copy|backup|bak|old|new|orig|original|final)\s*\d*$`), // foo-copy2、foo final（必须有分隔符，免得 renew 变 re）
	regexp.MustCompile(`(?i)[._-]bak[._-][\p{L}\p{N}_]+$`),                                 // foo.bak-20261101
	regexp.MustCompile(`[\s_-]?\d{8}[-_T]?\d{4,6}$`),                                       // foo-20261103-2315
	regexp.MustCompile(`[\s_-]?(19|20)\d{6}$`),                                             // foo-20261103
	regexp.MustCompile(`[\s_-]\d{6}$`),                                                     // foo-261103
	regexp.MustCompile(`(?i)[\s_-]v\d+(\.\d+)*$`),                                          // foo-v3、foo v1.2
	regexp.MustCompile(`[\s_-]\d+\.\d+(\.\d+)*$`),                                          // foo-1.2
	regexp.MustCompile(`(?i)[\s_-]r\d+$`),                                                  // foo-r4
	regexp.MustCompile(`[\s_-]\d{1,2}$`),                                                   // foo-2、foo 2、foo-02
}

var shortSHA = regexp.MustCompile(`(?i)[-_]([0-9a-f]{7,12})$`)
var hasDigit = regexp.MustCompile(`\d`)
var hasHexLetter = regexp.MustCompile(`(?i)[a-f]`)

// archiveExts 是会被当成「整体」看的扩展名（去后缀前先去掉）。
var stripExts = map[string]bool{
	".zip": true, ".tar": true, ".gz": true, ".tgz": true, ".7z": true, ".rar": true,
	".dmg": true, ".bak": true,
}

// StripVersion 去掉名字末尾的副本/版本后缀，保留原来的大小写。
// 例如 "agent-login-handoff-r4" -> "agent-login-handoff"，"报告 副本 (2)" -> "报告"。
func StripVersion(name string) string {
	s := strings.TrimSpace(name)
	for i := 0; i < 6; i++ {
		before := s
		for _, re := range suffixRules {
			if loc := re.FindStringIndex(s); loc != nil && loc[0] > 0 {
				s = strings.TrimSpace(s[:loc[0]])
			}
		}
		if m := shortSHA.FindStringSubmatchIndex(s); m != nil && m[0] > 0 {
			hex := s[m[2]:m[3]]
			if hasDigit.MatchString(hex) && hasHexLetter.MatchString(hex) {
				s = strings.TrimSpace(s[:m[0]])
			}
		}
		s = strings.TrimRight(s, " ._-")
		if s == before || s == "" {
			break
		}
	}
	if s == "" {
		return strings.TrimSpace(name)
	}
	return s
}

// CopyKey 是比较用的键：去扩展名（只去压缩包这类）、去版本后缀、转小写。
func CopyKey(name string) string {
	n := name
	for {
		ext := strings.ToLower(filepath.Ext(n))
		if ext == "" || !stripExts[ext] || len(n) == len(ext) {
			break
		}
		n = n[:len(n)-len(ext)]
	}
	return strings.ToLower(StripVersion(n))
}

// FileKey 是散落文件的比较键：去掉任意扩展名后再求 CopyKey。
func FileKey(name string) string {
	n := name
	if ext := filepath.Ext(n); ext != "" && len(ext) < len(n) {
		n = n[:len(n)-len(ext)]
	}
	return CopyKey(n)
}
