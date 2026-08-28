package router

import (
	"errors"
	"testing"
)

func TestDhAnyPermGranted(t *testing.T) {
	granted := func(names ...string) func(string) (bool, error) {
		set := make(map[string]struct{}, len(names))
		for _, name := range names {
			set[name] = struct{}{}
		}
		return func(operation string) (bool, error) {
			_, ok := set[operation]
			return ok, nil
		}
	}

	cases := []struct {
		name       string
		operations []string
		check      func(string) (bool, error)
		want       bool
	}{
		{"first matches", []string{"/trace/explorer", "/service"}, granted("/trace/explorer"), true},
		{"second matches", []string{"/trace/explorer", "/service"}, granted("/service"), true},
		{"both match", []string{"/trace/explorer", "/service"}, granted("/trace/explorer", "/service"), true},
		{"none matches", []string{"/trace/explorer", "/service"}, granted("/dashboards"), false},
		{"no perm at all", []string{"/trace/explorer", "/service"}, granted(), false},
		{"empty operation list", nil, granted("/trace/explorer"), false},
		{"blank operations are not a wildcard", []string{"", " "}, granted(""), false},
	}

	for _, c := range cases {
		got, err := dhAnyPermGranted(c.operations, c.check)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got != c.want {
			t.Fatalf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

// 查询出错必须冒泡（中间件会走 ginx.Dangerous 返回 500），不能被当成「没权限」或「有权限」。
func TestDhAnyPermGrantedPropagatesError(t *testing.T) {
	boom := errors.New("db down")
	got, err := dhAnyPermGranted([]string{"/trace/explorer", "/service"}, func(string) (bool, error) {
		return false, boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}
	if got {
		t.Fatal("must not grant on error")
	}
}

// 前一个权限点报错时不能继续问下一个：官方 rt.perm 遇到查询错误就 500，OR 版本不该把错误吞掉。
func TestDhAnyPermGrantedStopsAtFirstError(t *testing.T) {
	calls := 0
	_, err := dhAnyPermGranted([]string{"/trace/explorer", "/service"}, func(string) (bool, error) {
		calls++
		return false, errors.New("db down")
	})
	if err == nil {
		t.Fatal("want error")
	}
	if calls != 1 {
		t.Fatalf("checkPerm called %d times, want 1", calls)
	}
}
