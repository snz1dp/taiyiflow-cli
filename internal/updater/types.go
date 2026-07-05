package updater

// CheckUpdateRequest 对应后端 ClientReleaseCheckUpdateRequest。
type CheckUpdateRequest struct {
	Platform       string `json:"platform"`
	CurrentVersion string `json:"current_version"`
	DeviceID       string `json:"device_id,omitempty"`
	OwnerAppID     string `json:"owner_appid,omitempty"`
	FileType       string `json:"file_type,omitempty"`
	ClientGroup    string `json:"client_group,omitempty"`
}

// CheckUpdateResponse 对应后端 ClientReleaseCheckUpdateResponse。
type CheckUpdateResponse struct {
	HasUpdate    bool   `json:"has_update"`
	ForceUpdate  bool   `json:"force_update"`
	UpdatePolicy string `json:"update_policy"`
	AutoRestart  bool   `json:"auto_restart"`
	DownloadURL  string `json:"download_url"`
	DeliveryID   string `json:"delivery_id"`
	Version      string `json:"version"`
	FileName     string `json:"file_name"`
	FileSize     int64  `json:"file_size"`
	FileSHA256   string `json:"file_sha256"`
	Changelog    string `json:"changelog"`
	MinVersion   string `json:"min_version"`
}

// ReportAction 上报动作枚举。
type ReportAction string

const (
	ReportDownloadStarted  ReportAction = "download_started"
	ReportDownloadCompleted ReportAction = "download_completed"
	ReportDownloadFailed   ReportAction = "download_failed"
	ReportInstallStarted   ReportAction = "install_started"
	ReportInstallCompleted ReportAction = "install_completed"
	ReportInstallFailed    ReportAction = "install_failed"
)

// ReportStatusRequest 对应后端 ClientReleaseReportStatusRequest。
type ReportStatusRequest struct {
	DeliveryID   string `json:"delivery_id"`
	Action       string `json:"action"`
	DeviceID     string `json:"device_id,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
}

// ReportStatusResponse 对应后端 ClientReleaseReportStatusResponse。
type ReportStatusResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
}

// UpdatePolicy 更新策略。
type UpdatePolicy string

const (
	PolicySilent UpdatePolicy = "silent"
	PolicyNotify UpdatePolicy = "notify"
	PolicyForce  UpdatePolicy = "force"
)

// DownloadState 下载状态枚举。
type DownloadState string

const (
	StateDownloading DownloadState = "downloading"
	StateDownloaded  DownloadState = "downloaded"
	StateFailed      DownloadState = "failed"
)

// PersistState 持久化到磁盘的更新状态，用于跨重启恢复。
type PersistState struct {
	TargetVersion string        `json:"target_version,omitempty"`
	UpdatePolicy  string        `json:"update_policy,omitempty"`
	ForceUpdate   bool          `json:"force_update,omitempty"`
	DeliveryID    string        `json:"delivery_id,omitempty"`
	Download      *DownloadInfo `json:"download,omitempty"`
}

// DownloadInfo 下载文件信息。
type DownloadInfo struct {
	State    DownloadState `json:"state"`
	FileName string        `json:"file_name"`
	FilePath string        `json:"file_path"`
	FileSize int64         `json:"file_size"`
	FileSHA256 string      `json:"file_sha256"`
	Version  string        `json:"version"`
}
