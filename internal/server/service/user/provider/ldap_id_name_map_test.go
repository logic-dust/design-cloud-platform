package provider

import (
	"testing"

	"github.com/go-ldap/ldap/v3"
)

func entry(attrs map[string][]string) *ldap.Entry {
	e := &ldap.Entry{}
	for name, values := range attrs {
		e.Attributes = append(e.Attributes, &ldap.EntryAttribute{Name: name, Values: values})
	}
	return e
}

func TestIDNameMap_Normal(t *testing.T) {
	t.Parallel()
	got := idNameMap([]*ldap.Entry{
		entry(map[string][]string{"uidNumber": {"1000"}, "uid": {"alice"}}),
		entry(map[string][]string{"uidNumber": {"1001"}, "uid": {"bob"}}),
	}, "uidNumber", "uid")
	if len(got) != 2 || got[1000] != "alice" || got[1001] != "bob" {
		t.Fatalf("unexpected map: %v", got)
	}
}

// 目录数据异常（同一 idNumber 多条 entry）不属于本程序的职责：
// 只能来自绕过 IPA 唯一性检查的低层写入，请求侧去重无法预防，这里只做覆盖、不报错。
func TestIDNameMap_DuplicateIDOverwrites(t *testing.T) {
	t.Parallel()
	got := idNameMap([]*ldap.Entry{
		entry(map[string][]string{"uidNumber": {"904300001"}, "uid": {"dup1"}}),
		entry(map[string][]string{"uidNumber": {"904300001"}, "uid": {"dup2"}}),
	}, "uidNumber", "uid")
	if len(got) != 1 {
		t.Fatalf("expected 1 map entry, got %v", got)
	}
	if _, ok := got[904300001]; !ok {
		t.Fatalf("expected id 904300001 present, got %v", got)
	}
}

// 缺少 idNumber 或名字的条目被跳过。
func TestIDNameMap_SkipsIncompleteEntries(t *testing.T) {
	t.Parallel()
	got := idNameMap([]*ldap.Entry{
		entry(map[string][]string{"uid": {"no-uidnumber"}}),
		entry(map[string][]string{"uidNumber": {"1000"}}),
		entry(map[string][]string{"uidNumber": {"abc"}, "uid": {"bad-number"}}),
		entry(map[string][]string{"uidNumber": {"1001"}, "uid": {"bob"}}),
	}, "uidNumber", "uid")
	if len(got) != 1 || got[1001] != "bob" {
		t.Fatalf("unexpected map: %v", got)
	}
}

func TestIDNameMap_GroupsUseCN(t *testing.T) {
	t.Parallel()
	got := idNameMap([]*ldap.Entry{
		entry(map[string][]string{"gidNumber": {"998"}, "cn": {"wheel"}}),
	}, "gidNumber", "cn")
	if got[998] != "wheel" {
		t.Fatalf("unexpected map: %v", got)
	}
}
