package identity

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/service/user"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// callRecorder 记录跨域副作用的调用序列，用于断言编排顺序。
type callRecorder struct{ calls []string }

type fakeUserWriter struct {
	rec         *callRecorder
	deleteErr   error
	adminUpdate *model.User
	adminErr    error
}

func (f *fakeUserWriter) Delete(_ context.Context, username string) error {
	f.rec.calls = append(f.rec.calls, "user.Delete:"+username)
	return f.deleteErr
}

func (f *fakeUserWriter) AdminUpdate(_ context.Context, _ *model.Session, username string, _ user.AdminUserUpdateRequest) (*model.User, error) {
	f.rec.calls = append(f.rec.calls, "user.AdminUpdate:"+username)
	return f.adminUpdate, f.adminErr
}

type fakeRoleWriter struct {
	rec       *callRecorder
	updated   model.User
	updateErr error
}

func (f *fakeRoleWriter) SetUserRole(_ context.Context, username string, role model.Role) (model.User, error) {
	f.rec.calls = append(f.rec.calls, "role.SetUserRole:"+username)
	return f.updated, f.updateErr
}

type fakeConnPurger struct {
	rec *callRecorder
	err error
}

func (f *fakeConnPurger) PurgeUserConnections(_ context.Context, username string) error {
	f.rec.calls = append(f.rec.calls, "conn.Purge:"+username)
	return f.err
}

type fakeSessionRevoker struct {
	rec       *callRecorder
	deleteErr error
}

func (f *fakeSessionRevoker) InvalidateByUsername(username string) {
	f.rec.calls = append(f.rec.calls, "session.Invalidate:"+username)
}

func (f *fakeSessionRevoker) Delete(_ context.Context, username string) error {
	f.rec.calls = append(f.rec.calls, "session.Delete:"+username)
	return f.deleteErr
}

func newTestService(rec *callRecorder, u *fakeUserWriter, r *fakeRoleWriter, c *fakeConnPurger, se *fakeSessionRevoker) *Service {
	return New(u, r, c, se, slog.Default())
}

func TestDeleteUser_Order(t *testing.T) {
	t.Parallel()
	rec := &callRecorder{}
	svc := newTestService(rec, &fakeUserWriter{rec: rec}, &fakeRoleWriter{rec: rec}, &fakeConnPurger{rec: rec}, &fakeSessionRevoker{rec: rec})

	require.NoError(t, svc.DeleteUser(context.Background(), "alice"))
	assert.Equal(t, []string{"conn.Purge:alice", "session.Delete:alice", "user.Delete:alice"}, rec.calls)
}

func TestDeleteUser_PurgeErrorAborts(t *testing.T) {
	t.Parallel()
	rec := &callRecorder{}
	purgeErr := errors.New("purge failed")
	svc := newTestService(rec, &fakeUserWriter{rec: rec}, &fakeRoleWriter{rec: rec}, &fakeConnPurger{rec: rec, err: purgeErr}, &fakeSessionRevoker{rec: rec})

	err := svc.DeleteUser(context.Background(), "alice")
	require.ErrorIs(t, err, purgeErr)
	// 连接清理失败时用户行必须仍在（可重试）
	assert.Equal(t, []string{"conn.Purge:alice"}, rec.calls)
}

func TestDeleteUser_SessionErrorStillDeletesUser(t *testing.T) {
	t.Parallel()
	rec := &callRecorder{}
	svc := newTestService(rec, &fakeUserWriter{rec: rec}, &fakeRoleWriter{rec: rec}, &fakeConnPurger{rec: rec}, &fakeSessionRevoker{rec: rec, deleteErr: errors.New("session db down")})

	require.NoError(t, svc.DeleteUser(context.Background(), "alice"))
	assert.Equal(t, []string{"conn.Purge:alice", "session.Delete:alice", "user.Delete:alice"}, rec.calls)
}

func TestUpdateUserStatus_DisableKicksSessions(t *testing.T) {
	t.Parallel()
	rec := &callRecorder{}
	disabled := int(model.UserDisabled)
	uw := &fakeUserWriter{rec: rec, adminUpdate: &model.User{Username: "bob", Status: model.UserDisabled}}
	svc := newTestService(rec, uw, &fakeRoleWriter{rec: rec}, &fakeConnPurger{rec: rec}, &fakeSessionRevoker{rec: rec})

	updated, err := svc.UpdateUserStatus(context.Background(), &model.Session{Username: "admin"}, "bob", user.AdminUserUpdateRequest{Status: &disabled})
	require.NoError(t, err)
	require.NotNil(t, updated)
	assert.Equal(t, []string{"user.AdminUpdate:bob", "session.Delete:bob", "session.Invalidate:bob"}, rec.calls)
}

func TestUpdateUserStatus_NoStatusChangeOnlyInvalidates(t *testing.T) {
	t.Parallel()
	rec := &callRecorder{}
	download := false
	uw := &fakeUserWriter{rec: rec, adminUpdate: &model.User{Username: "bob", Status: model.UserActive}}
	svc := newTestService(rec, uw, &fakeRoleWriter{rec: rec}, &fakeConnPurger{rec: rec}, &fakeSessionRevoker{rec: rec})

	_, err := svc.UpdateUserStatus(context.Background(), &model.Session{Username: "admin"}, "bob", user.AdminUserUpdateRequest{DownloadAccess: &download})
	require.NoError(t, err)
	assert.Equal(t, []string{"user.AdminUpdate:bob", "session.Invalidate:bob"}, rec.calls)
}

func TestUpdateUserStatus_ErrorNoSideEffects(t *testing.T) {
	t.Parallel()
	rec := &callRecorder{}
	uw := &fakeUserWriter{rec: rec, adminErr: errors.New("forbidden")}
	svc := newTestService(rec, uw, &fakeRoleWriter{rec: rec}, &fakeConnPurger{rec: rec}, &fakeSessionRevoker{rec: rec})

	disabled := int(model.UserDisabled)
	_, err := svc.UpdateUserStatus(context.Background(), &model.Session{Username: "admin"}, "bob", user.AdminUserUpdateRequest{Status: &disabled})
	require.Error(t, err)
	assert.Equal(t, []string{"user.AdminUpdate:bob"}, rec.calls)
}

func TestAssignRole_SuccessEvictsSessions(t *testing.T) {
	t.Parallel()
	rec := &callRecorder{}
	rw := &fakeRoleWriter{rec: rec, updated: model.User{Username: "carol", Role: model.RoleSystemAdmin}}
	svc := newTestService(rec, &fakeUserWriter{rec: rec}, rw, &fakeConnPurger{rec: rec}, &fakeSessionRevoker{rec: rec})

	updated, err := svc.AssignRole(context.Background(), "carol", model.RoleSystemAdmin)
	require.NoError(t, err)
	assert.Equal(t, model.RoleSystemAdmin, updated.Role)
	assert.Equal(t, []string{"role.SetUserRole:carol", "session.Delete:carol"}, rec.calls)
}

func TestAssignRole_RoleErrorNoEviction(t *testing.T) {
	t.Parallel()
	rec := &callRecorder{}
	rw := &fakeRoleWriter{rec: rec, updateErr: errors.New("invalid role")}
	svc := newTestService(rec, &fakeUserWriter{rec: rec}, rw, &fakeConnPurger{rec: rec}, &fakeSessionRevoker{rec: rec})

	_, err := svc.AssignRole(context.Background(), "carol", model.Role(99))
	require.Error(t, err)
	assert.Equal(t, []string{"role.SetUserRole:carol"}, rec.calls)
}

func TestAssignRole_EvictionErrorPropagates(t *testing.T) {
	t.Parallel()
	rec := &callRecorder{}
	evictErr := errors.New("session db down")
	rw := &fakeRoleWriter{rec: rec, updated: model.User{Username: "carol"}}
	svc := newTestService(rec, &fakeUserWriter{rec: rec}, rw, &fakeConnPurger{rec: rec}, &fakeSessionRevoker{rec: rec, deleteErr: evictErr})

	_, err := svc.AssignRole(context.Background(), "carol", model.RoleNormal)
	require.ErrorIs(t, err, evictErr)
}
