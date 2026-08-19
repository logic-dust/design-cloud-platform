package payload

// --- User Actions ---

// UserPasswordChange is the payload for USER_PASSWORD_CHANGE
type UserPasswordChange struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	NewPassword string `json:"newPassword"`
}

// UserPasswordReset is the payload for USER_PASSWORD_RESET (Admin resets user password)
type UserPasswordReset struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// UserPasswordVerify is the payload for USER_PASSWORD_VERIFY (Login check)
type UserPasswordVerify struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// UserList for USER_LIST
type UserList struct {
	MinUID *int `json:"minUid,omitempty"`
	MaxUID *int `json:"maxUid,omitempty"`
}

// UserGet for USER_GET
type UserGet struct {
	Username string `json:"username"`
}

// GroupList for GROUP_LIST
type GroupList struct {
	MinGID *int `json:"minGid,omitempty"`
	MaxGID *int `json:"maxGid,omitempty"`
}

// GroupGet for GROUP_GET
type GroupGet struct {
	GroupName string `json:"groupName"`
}
