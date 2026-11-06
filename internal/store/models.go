package store

// Domain models mirror the database rows. Optional columns use pointers so API
// serialization can distinguish "absent" from zero values.

type User struct {
	ID            string  `json:"id"`
	Email         string  `json:"email"`
	PasswordHash  string  `json:"-"`
	TOTPSecretEnc *string `json:"-"`
	Role          string  `json:"role"`
	Disabled      bool    `json:"disabled"`
	CreatedAt     int64   `json:"created_at"`
	UpdatedAt     int64   `json:"updated_at"`
}

type Session struct {
	ID         string
	UserID     string
	TokenHash  []byte
	CSRFSecret string
	ExpiresAt  int64
	CreatedAt  int64
	LastSeenAt int64
	IP         string
	UserAgent  string
}

type CAAccount struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	DirectoryURL  string  `json:"directory_url"`
	Email         *string `json:"email"`
	EABKID        *string `json:"eab_kid"`
	EABHMACEnc    *string `json:"-"`
	TOSAcceptedAt *int64  `json:"tos_accepted_at"`
	KeyVersion    int     `json:"-"`
	Status        string  `json:"status"`
	LastError     *string `json:"last_error"`
	CreatedAt     int64   `json:"created_at"`
	UpdatedAt     int64   `json:"updated_at"`
}

type DNSCredential struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Provider   string  `json:"provider"`
	ConfigEnc  string  `json:"-"`
	KeyVersion int     `json:"-"`
	Status     string  `json:"status"`
	LastError  *string `json:"last_error"`
	LastUsedAt *int64  `json:"last_used_at"`
	CreatedAt  int64   `json:"created_at"`
	UpdatedAt  int64   `json:"updated_at"`
}

type Certificate struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	PrimaryDomain     string   `json:"primary_domain"`
	SAN               []string `json:"san"`
	KeyType           string   `json:"key_type"`
	CAAccountID       string   `json:"ca_account_id"`
	ChallengeType     string   `json:"challenge_type"`
	DNSCredentialID   *string  `json:"dns_credential_id"`
	Webroot           *string  `json:"webroot"`
	Options           Options  `json:"options"`
	AutoRenew         bool     `json:"auto_renew"`
	Status            string   `json:"status"`
	CurrentRevisionID *string  `json:"current_revision_id"`
	NextRenewAt       *int64   `json:"next_renew_at"`
	LastErrorCode     *string  `json:"last_error_code"`
	LastErrorMessage  *string  `json:"last_error_message"`
	Version           int      `json:"version"`
	DeletedAt         *int64   `json:"deleted_at"`
	CreatedAt         int64    `json:"created_at"`
	UpdatedAt         int64    `json:"updated_at"`
}

// Options carries additional acme.sh switches that are safe to expose.
type Options struct {
	PreferredChain string `json:"preferred_chain,omitempty"`
	DNSAlias       string `json:"dns_alias,omitempty"`
	DNSSleep       int    `json:"dns_sleep,omitempty"`
	AlwaysNewKey   bool   `json:"always_new_key,omitempty"`
	MustStaple     bool   `json:"must_staple,omitempty"`
}

type Revision struct {
	ID                string `json:"id"`
	CertificateID     string `json:"certificate_id"`
	Serial            string `json:"serial"`
	FingerprintSHA256 string `json:"fingerprint_sha256"`
	NotBefore         int64  `json:"not_before"`
	NotAfter          int64  `json:"not_after"`
	CertPath          string `json:"-"`
	KeyPath           string `json:"-"`
	ChainPath         string `json:"-"`
	FullchainPath     string `json:"-"`
	ConfigHash        string `json:"-"`
	IssuedAt          int64  `json:"issued_at"`
	RevokedAt         *int64 `json:"revoked_at"`
	RevokeReason      *int64 `json:"revoke_reason"`
	CreatedAt         int64  `json:"created_at"`
}

type Job struct {
	ID             string  `json:"id"`
	Type           string  `json:"type"`
	CertificateID  *string `json:"certificate_id"`
	CAAccountID    *string `json:"ca_account_id"`
	Status         string  `json:"status"`
	Attempts       int     `json:"attempts"`
	MaxAttempts    int     `json:"max_attempts"`
	PayloadJSON    string  `json:"-"`
	IdempotencyKey *string `json:"-"`
	ErrorCode      *string `json:"error_code"`
	ErrorMessage   *string `json:"error_message"`
	LogPath        *string `json:"-"`
	CreatedAt      int64   `json:"created_at"`
	StartedAt      *int64  `json:"started_at"`
	FinishedAt     *int64  `json:"finished_at"`
	HeartbeatAt    *int64  `json:"heartbeat_at"`
	AvailableAt    int64   `json:"-"`
}

type Event struct {
	ID            int64   `json:"id"`
	CertificateID string  `json:"certificate_id"`
	Type          string  `json:"type"`
	Message       *string `json:"message"`
	JobID         *string `json:"job_id"`
	CreatedAt     int64   `json:"created_at"`
}

type Deployment struct {
	ID            string  `json:"id"`
	CertificateID string  `json:"certificate_id"`
	Name          string  `json:"name"`
	Type          string  `json:"type"`
	ConfigEnc     string  `json:"-"`
	KeyVersion    int     `json:"-"`
	Enabled       bool    `json:"enabled"`
	LastRunAt     *int64  `json:"last_run_at"`
	LastStatus    *string `json:"last_status"`
	CreatedAt     int64   `json:"created_at"`
	UpdatedAt     int64   `json:"updated_at"`
}

type DeploymentRun struct {
	ID           string  `json:"id"`
	DeploymentID string  `json:"deployment_id"`
	RevisionID   string  `json:"revision_id"`
	Status       string  `json:"status"`
	Message      *string `json:"message"`
	CreatedAt    int64   `json:"created_at"`
}

type NotificationChannel struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Type       string `json:"type"`
	ConfigEnc  string `json:"-"`
	KeyVersion int    `json:"-"`
	EventsJSON string `json:"-"`
	Enabled    bool   `json:"enabled"`
	CreatedAt  int64  `json:"created_at"`
	UpdatedAt  int64  `json:"updated_at"`
}

// Workflow is a user-defined certificate pipeline.
type Workflow struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Description    string `json:"description"`
	DefinitionJSON string `json:"-"`
	Enabled        bool   `json:"enabled"`
	TriggerType    string `json:"trigger_type"`
	Schedule       string `json:"schedule"`
	CreatedAt      int64  `json:"created_at"`
	UpdatedAt      int64  `json:"updated_at"`
}

// WorkflowRun is one durable execution of a workflow snapshot.
type WorkflowRun struct {
	ID             string  `json:"id"`
	WorkflowID     string  `json:"workflow_id"`
	Status         string  `json:"status"`
	TriggerType    string  `json:"trigger_type"`
	DefinitionJSON string  `json:"-"`
	Owner          string  `json:"-"`
	Error          *string `json:"error"`
	StartedAt      *int64  `json:"started_at"`
	FinishedAt     *int64  `json:"finished_at"`
	HeartbeatAt    *int64  `json:"heartbeat_at"`
	CreatedAt      int64   `json:"created_at"`
}

// WorkflowStepRun is the durable state of one step in a workflow run.
type WorkflowStepRun struct {
	ID          string  `json:"id"`
	RunID       string  `json:"run_id"`
	StepID      string  `json:"step_id"`
	Type        string  `json:"type"`
	Name        string  `json:"name"`
	Status      string  `json:"status"`
	Attempts    int     `json:"attempts"`
	JobIDsJSON  string  `json:"-"`
	OutputJSON  string  `json:"output_json"`
	Error       *string `json:"error"`
	StartedAt   *int64  `json:"started_at"`
	FinishedAt  *int64  `json:"finished_at"`
	AvailableAt int64   `json:"available_at"`
	CreatedAt   int64   `json:"created_at"`
}

type AuditLog struct {
	ID           string  `json:"id"`
	ActorID      *string `json:"actor_id"`
	Action       string  `json:"action"`
	ResourceType string  `json:"resource_type"`
	ResourceID   *string `json:"resource_id"`
	IP           *string `json:"ip"`
	UserAgent    *string `json:"user_agent"`
	RequestID    *string `json:"request_id"`
	Result       string  `json:"result"`
	DetailJSON   *string `json:"detail_json"`
	CreatedAt    int64   `json:"created_at"`
}
