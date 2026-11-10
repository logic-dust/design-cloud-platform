package payload

// FileInfo represents file metadata returned by FILE_LIST and other file operations.
// This is the canonical file information structure shared across PrivilegedExec and service layers.
type FileInfo struct {
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	Mode       string `json:"mode"`
	ModTime    int64  `json:"modTime"`
	IsDir      bool   `json:"isDir"`
	Hidden     bool   `json:"hidden"`
	OwnerID    uint32 `json:"ownerId"`
	GroupID    uint32 `json:"groupId"`
	IsSymlink  bool   `json:"isSymlink"`
	LinkTarget string `json:"linkTarget,omitempty"`
}

// FileList for FILE_LIST.
// Limit is required and must be > 0 (Portal fills it from file.listMaxEntries).
// The PrivilegedExec returns up to Limit+100 names; it does not clamp to settings bounds.
type FileList struct {
	Dir   string `json:"dir"`
	Limit int    `json:"limit,omitempty"`
}

// FileRead for FILE_READ
type FileRead struct {
	Path string `json:"path"`
}

// FileDelete for FILE_DELETE
type FileDelete struct {
	Dir   string   `json:"dir"`
	Files []string `json:"files"`
}

// FileMove for FILE_MOVE
type FileMove struct {
	Src string `json:"src"`
	Dst string `json:"dst"`
}

// FileRename for FILE_RENAME
type FileRename struct {
	Src  string `json:"src"`
	Name string `json:"name"`
}

// FileMkdir for FILE_MKDIR
type FileMkdir struct {
	Dir string `json:"dir"`
}

// FileMkdirAll for FILE_MKDIR_ALL
type FileMkdirAll struct {
	Dir string `json:"dir"`
}

// FileCheck for FILE_CHECK (Read check)
type FileCheck struct {
	Path string `json:"path"`
}

type FileCheckResult struct {
	Size     int64  `json:"size"`
	Mode     string `json:"mode"`
	RealPath string `json:"realPath,omitempty"`
	ModTime  int64  `json:"modTime"`
	ModTimeN int64  `json:"modTimeN"`
	CTime    int64  `json:"cTime"`
	CTimeN   int64  `json:"cTimeN"`
	Device   uint64 `json:"device"`
	Inode    uint64 `json:"inode"`
	IsDir    bool   `json:"isDir"`
}

// FileWriteCheck for FILE_WRITE_CHECK
type FileWriteCheck struct {
	Dir string `json:"dir"`
}

// FileUpload for FILE_UPLOAD
// The file body is NOT in this payload; it follows the JSON header in stdin.
type FileUpload struct {
	DstPath string `json:"dstPath"`
	Size    *int64 `json:"size"`
}

// FileDownload is the FILE_DOWNLOAD streaming action payload (node-local file bytes
// to stdout). It is not the Portal download-approval model (model.DownloadRequest /
// table download_requests).
type FileDownload struct {
	SrcPath string `json:"srcPath"`
	Size    *int64 `json:"size"`
}

// TrashMove for TRASH_MOVE
type TrashMove struct {
	Dir      string   `json:"dir"`
	Files    []string `json:"files"`
	MaxBytes int64    `json:"maxBytes,omitempty"`
	MaxCount int      `json:"maxCount,omitempty"`
}

// TrashDelete for TRASH_DELETE
type TrashDelete struct {
	TrashName string `json:"trashName"`
}

// TrashDeleteMany for TRASH_DELETE_MANY
type TrashDeleteMany struct {
	TrashNames []string `json:"trashNames"`
}

// TrashRestoreMany for TRASH_RESTORE_MANY
type TrashRestoreMany struct {
	TrashNames []string `json:"trashNames"`
}

// TrashEmpty for TRASH_EMPTY
type TrashEmpty struct{}

// TrashList for TRASH_LIST.
// Limit is required and must be > 0 (Portal fills it from file.listMaxEntries).
// The PrivilegedExec returns up to Limit+100 names; it does not clamp to settings bounds.
type TrashList struct {
	TrashPath string `json:"trashPath"`
	Limit     int    `json:"limit,omitempty"`
}
