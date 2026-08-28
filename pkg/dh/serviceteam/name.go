package serviceteam

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// 所属业务与团队等同，暂不拆。名称即日志索引前缀。
var teamNameRe = regexp.MustCompile(`^[a-z]+(-[a-z]+)*$`)

var ErrInvalidTeamName = errors.New("所属业务名称仅允许小写字母和连字符，例如 turms 或 rome-sec")

var ErrMultipleTeams = errors.New("一个服务只能绑定一个所属业务")

func ValidateTeamName(name string) error {
	if !teamNameRe.MatchString(strings.TrimSpace(name)) {
		return ErrInvalidTeamName
	}
	return nil
}

func ErrAlreadyBound(service, team string) error {
	service = strings.TrimSpace(service)
	team = strings.TrimSpace(team)
	if team == "" {
		return fmt.Errorf("服务 %s 已绑定其他所属业务，一个服务只能绑定一个所属业务", service)
	}
	return fmt.Errorf("服务 %s 已绑定所属业务 %s，一个服务只能绑定一个所属业务", service, team)
}
