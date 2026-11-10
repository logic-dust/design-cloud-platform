package provider

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"design-cloud-engine/internal/execution/payload"

	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/model"
)

type localProvider struct {
	db      *db.UserRepo
	execSvc execution.Executor
	logger  *slog.Logger
}

func parseUserListLine(line string) SystemUser {
	infoList := strings.Split(line, ":")
	if len(infoList) < 7 {
		return SystemUser{}
	}
	return SystemUser{
		Username: infoList[0],
		Uid:      infoList[2],
		Gid:      infoList[3],
		Home:     infoList[5],
		Shell:    infoList[6],
		Status:   model.UserStatusString(model.UserActive),
	}
}

func parseUserGetOutput(raw string) (SystemUser, error) {
	// Raw output is from getent passwd + group information from /etc/group
	// This will be handled differently, we'll parse getent passwd directly.
	parts := strings.SplitN(strings.TrimSpace(raw), ":", 7)
	if len(parts) < 7 {
		return SystemUser{}, fmt.Errorf("invalid USER_GET output")
	}
	return SystemUser{
		Username: parts[0],
		Uid:      parts[2],
		Gid:      parts[3],
		Comment:  parts[4],
		Home:     parts[5],
		Shell:    parts[6],
		Status:   model.UserStatusString(model.UserActive),
	}, nil
}

func splitGroups(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	return strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t'
	})
}

func isIDInRange(idStr string, minVal, maxVal *int) bool {
	id, err := strconv.Atoi(idStr)
	if err != nil {
		return false
	}
	if minVal != nil && id < *minVal {
		return false
	}
	if maxVal != nil && id > *maxVal {
		return false
	}
	return true
}

func (p *localProvider) runAction(ctx context.Context, actionName execution.ActionName, payload any) (string, error) {
	executionID := fmt.Sprintf("%s-%d", actionName, time.Now().UnixNano())
	req := execution.NewExecuteRequest(actionName, executionID, execution.WithTargetUser(""), execution.WithPayload(payload))
	return execution.Run(ctx, p.execSvc, req)
}

func (p *localProvider) GetUser(ctx context.Context, username string) (*SystemUser, error) {
	rawUserInfoStr, err := p.runAction(ctx, execution.ActionUserGet, payload.UserGet{Username: username})
	if err != nil {
		return nil, err
	}
	userInfo, err := parseUserGetOutput(rawUserInfoStr)
	if err != nil {
		return nil, err
	}
	if dbUser, err := p.db.GetByName(ctx, username); err == nil && dbUser.ID != 0 {
		userInfo.ID = dbUser.ID
	}
	return &userInfo, nil
}

func (p *localProvider) ListUsers(ctx context.Context) ([]SystemUser, error) {
	rawUsersInfoStr, err := p.runAction(ctx, execution.ActionUserList, payload.UserList{})
	if err != nil {
		return nil, err
	}
	usersMap := make(map[string]SystemUser)
	userGroupsMap, err := p.ListUserGroups(ctx)
	if err != nil {
		return nil, err
	}
	minUid := 1000
	for passwdLine := range strings.SplitSeq(strings.TrimRight(rawUsersInfoStr, "\n"), "\n") {
		u := parseUserListLine(passwdLine)
		if u.Username == "" || u.Username == "nobody" {
			continue
		}
		if !isIDInRange(u.Uid, &minUid, nil) {
			continue
		}
		if groups, ok := userGroupsMap[u.Username]; ok {
			u.Groups = groups
		}
		usersMap[u.Username] = u
	}
	if dbUsers, err := p.db.ListAll(ctx); err == nil {
		for _, u := range dbUsers {
			if info, ok := usersMap[u.Username]; ok {
				info.ID = u.ID
				usersMap[u.Username] = info
			}
		}
	}
	return slices.Collect(maps.Values(usersMap)), nil
}

// UsersByIDs resolves uid → username from the local passwd snapshot.
// Unlike ListUsers, system accounts with uid < 1000 are included (file owners often use them).
func (p *localProvider) UsersByIDs(ctx context.Context, uids []uint32) (map[uint32]string, error) {
	if len(uids) == 0 {
		return map[uint32]string{}, nil
	}
	want := make(map[uint32]struct{}, len(uids))
	for _, id := range uids {
		want[id] = struct{}{}
	}
	rawUsersInfoStr, err := p.runAction(ctx, execution.ActionUserList, payload.UserList{})
	if err != nil {
		return nil, err
	}
	out := make(map[uint32]string)
	for passwdLine := range strings.SplitSeq(strings.TrimRight(rawUsersInfoStr, "\n"), "\n") {
		u := parseUserListLine(passwdLine)
		if u.Username == "" {
			continue
		}
		id, err := strconv.ParseUint(u.Uid, 10, 32)
		if err != nil {
			continue
		}
		uid := uint32(id)
		if _, ok := want[uid]; ok {
			out[uid] = u.Username
		}
	}
	return out, nil
}

// GroupsByIDs resolves gid → group name from the local group snapshot (no gid >= 1000 filter).
func (p *localProvider) GroupsByIDs(ctx context.Context, gids []uint32) (map[uint32]string, error) {
	if len(gids) == 0 {
		return map[uint32]string{}, nil
	}
	want := make(map[uint32]struct{}, len(gids))
	for _, id := range gids {
		want[id] = struct{}{}
	}
	groupsInfo, err := p.runAction(ctx, execution.ActionGroupList, payload.GroupList{})
	if err != nil {
		return nil, err
	}
	out := make(map[uint32]string)
	for groupInfo := range strings.SplitSeq(strings.TrimRight(groupsInfo, "\n"), "\n") {
		infoList := strings.Split(groupInfo, ":")
		if len(infoList) < 3 {
			continue
		}
		id, err := strconv.ParseUint(infoList[2], 10, 32)
		if err != nil {
			continue
		}
		gid := uint32(id)
		if _, ok := want[gid]; ok {
			out[gid] = infoList[0]
		}
	}
	return out, nil
}

func (p *localProvider) ListGroups(ctx context.Context) (map[string][]string, error) {
	groupsInfo, err := p.runAction(ctx, execution.ActionGroupList, payload.GroupList{})
	if err != nil {
		return nil, err
	}
	groupsMap := make(map[string][]string)
	minGid := 1000
	for groupInfo := range strings.SplitSeq(strings.TrimRight(groupsInfo, "\n"), "\n") {
		infoList := strings.Split(groupInfo, ":")
		if len(infoList) < 3 {
			continue
		}
		if !isIDInRange(infoList[2], &minGid, nil) {
			continue
		}
		if len(infoList) < 4 || infoList[3] == "" {
			groupsMap[infoList[0]] = nil
			continue
		}
		groupsMap[infoList[0]] = splitGroups(infoList[3])
	}
	return groupsMap, nil
}

func (p *localProvider) GetGroupMembers(ctx context.Context, groupName string) ([]string, error) {
	groupsMap, err := p.ListGroups(ctx)
	if err != nil {
		return nil, err
	}
	if members, ok := groupsMap[groupName]; ok {
		return members, nil
	}
	return nil, ErrGroupNotFound
}

func (p *localProvider) ListUserGroups(ctx context.Context) (map[string][]string, error) {
	groupsInfo, err := p.runAction(ctx, execution.ActionGroupList, payload.GroupList{})
	if err != nil {
		return nil, err
	}
	userGroupsMap := make(map[string][]string)
	for groupInfo := range strings.SplitSeq(strings.TrimRight(groupsInfo, "\n"), "\n") {
		infoList := strings.Split(groupInfo, ":")
		if len(infoList) < 4 {
			continue
		}
		groupName := infoList[0]
		users := infoList[3]
		if users != "" {
			for u := range strings.SplitSeq(users, ",") {
				if u != "" {
					userGroupsMap[u] = append(userGroupsMap[u], groupName)
				}
			}
		}
	}
	return userGroupsMap, nil
}

func (p *localProvider) VerifyPassword(ctx context.Context, username, password string) error {
	executionID := fmt.Sprintf("user-verify-pwd-%s-%d", username, time.Now().UnixNano())
	req := execution.NewExecuteRequest(execution.ActionUserPasswordVerify, executionID, execution.WithPayload(payload.UserPasswordVerify{
		Username: username,
		Password: password,
	}))
	_, err := execution.Run(ctx, p.execSvc, req)
	if err != nil {
		return ErrUserPasswordIncorrect
	}
	return nil
}

func (p *localProvider) ChangePassword(ctx context.Context, username, oldPassword, newPassword string) error {
	executionID := fmt.Sprintf("user-change-pwd-%s-%d", username, time.Now().UnixNano())
	req := execution.NewExecuteRequest(execution.ActionUserPasswordChange, executionID,
		execution.WithTargetUser(""),
		execution.WithPayload(payload.UserPasswordChange{Username: username, Password: oldPassword, NewPassword: newPassword}),
	)
	_, err := execution.Run(ctx, p.execSvc, req)
	return err
}

func (p *localProvider) ResetPassword(ctx context.Context, username, newPassword string) error {
	executionID := fmt.Sprintf("user-reset-pwd-%s-%d", username, time.Now().UnixNano())
	req := execution.NewExecuteRequest(execution.ActionUserPasswordReset, executionID,
		execution.WithTargetUser(""),
		execution.WithPayload(payload.UserPasswordReset{Username: username, Password: newPassword}),
	)
	_, err := execution.Run(ctx, p.execSvc, req)
	if err != nil {
		return err
	}
	return nil
}
