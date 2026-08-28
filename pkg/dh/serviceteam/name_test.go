package serviceteam

import "testing"

func TestValidateTeamName(t *testing.T) {
	ok := []string{"turms", "rome-sec", "aa-bb-cc", "a", " turms "}
	for _, name := range ok {
		if err := ValidateTeamName(name); err != nil {
			t.Fatalf("ValidateTeamName(%q) = %v", name, err)
		}
	}
	bad := []string{"", "turms1", "turms_prod", "Turms", "turms--a", "-turms", "turms-", "业务", "turms.prod"}
	for _, name := range bad {
		if err := ValidateTeamName(name); err != ErrInvalidTeamName {
			t.Fatalf("ValidateTeamName(%q) = %v, want ErrInvalidTeamName", name, err)
		}
	}
}

func TestErrAlreadyBound(t *testing.T) {
	got := ErrAlreadyBound("turms-gateway", "turms").Error()
	if got != "服务 turms-gateway 已绑定所属业务 turms，一个服务只能绑定一个所属业务" {
		t.Fatalf("got %q", got)
	}
	got = ErrAlreadyBound("turms-gateway", "").Error()
	if got != "服务 turms-gateway 已绑定其他所属业务，一个服务只能绑定一个所属业务" {
		t.Fatalf("got %q", got)
	}
}
