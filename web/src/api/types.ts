export type Role = "admin" | "operator" | "viewer";

export interface User {
  id: string;
  email: string;
  role: Role;
  disabled: boolean;
  totp_enabled: boolean;
  created_at: number;
}

export interface CAAccount {
  id: string;
  name: string;
  directory_url: string;
  email: string | null;
  eab_kid: string | null;
  status: "new" | "registered" | "error" | "disabled";
  last_error: string | null;
  created_at: number;
  updated_at: number;
}

export interface CAPreset {
  id: string;
  name: string;
  directory_url: string;
  needs_eab: boolean;
}

export interface DNSField {
  key: string;
  label: string;
  secret: boolean;
  required: boolean;
  help?: string;
}

export interface DNSProvider {
  id: string;
  name: string;
  docs: string;
  min_role: string;
  fields: DNSField[];
  wildcard: boolean;
}

export interface DNSCredential {
  id: string;
  name: string;
  provider: string;
  status: "unverified" | "ok" | "error";
  last_error: string | null;
  last_used_at: number | null;
  configured_fields: string[];
  created_at: number;
  updated_at: number;
}

export type ChallengeType = "dns-01" | "http-01-webroot" | "http-01-standalone" | "tls-alpn-01";
export type CertificateStatus = "draft" | "queued" | "issuing" | "active" | "renewing" | "failed" | "revoked" | "disabled";

export interface CertificateOptions {
  preferred_chain?: string;
  dns_alias?: string;
  dns_sleep?: number;
  always_new_key?: boolean;
  must_staple?: boolean;
}

export interface Certificate {
  id: string;
  name: string;
  primary_domain: string;
  san: string[];
  key_type: string;
  ca_account_id: string;
  challenge_type: ChallengeType;
  dns_credential_id: string | null;
  webroot: string | null;
  options: CertificateOptions;
  auto_renew: boolean;
  auto_deploy: boolean;
  status: CertificateStatus;
  current_revision_id: string | null;
  next_renew_at: number | null;
  last_error_code: string | null;
  last_error_message: string | null;
  version: number;
  deleted_at: number | null;
  created_at: number;
  updated_at: number;
}

export interface Revision {
  id: string;
  certificate_id: string;
  serial: string;
  fingerprint_sha256: string;
  not_before: number;
  not_after: number;
  issued_at: number;
  revoked_at: number | null;
  revoke_reason: number | null;
  created_at: number;
}

export interface Job {
  id: string;
  type: "register_account" | "issue" | "renew" | "revoke" | "deploy" | "sync";
  certificate_id: string | null;
  ca_account_id: string | null;
  status: "queued" | "running" | "succeeded" | "failed" | "canceled" | "interrupted";
  attempts: number;
  max_attempts: number;
  error_code: string | null;
  error_message: string | null;
  created_at: number;
  started_at: number | null;
  finished_at: number | null;
  heartbeat_at: number | null;
}

export interface CertEvent {
  id: number;
  certificate_id: string;
  type: string;
  message: string | null;
  job_id: string | null;
  created_at: number;
}

export type DeploymentType =
  | "local_file"
  | "reload"
  | "webhook"
  | "ssh"
  | "baota_panel"
  | "baota_site"
  | "1panel_panel"
  | "1panel_site"
  | "tencent_cdn"
  | "tencent_waf"
  | "tencent_cos"
  | "tencent_teo"
  | "aliyun_cdn"
  | "aliyun_waf"
  | "aliyun_oss"
  | "aliyun_dcdn"
  | "qiniu_cdn";

export interface Deployment {
  id: string;
  certificate_id: string;
  name: string;
  type: DeploymentType;
  credential_id: string | null;
  enabled: boolean;
  last_run_at: number | null;
  last_status: string | null;
  created_at: number;
  updated_at: number;
}

export type DeploymentCredentialType = "tencent" | "aliyun" | "qiniu" | "baota" | "1panel" | "ssh";

export interface DeploymentCredential {
  id: string;
  name: string;
  type: DeploymentCredentialType;
  configured_fields: string[];
  public_config: Record<string, string | number | boolean>;
  created_at: number;
  updated_at: number;
}

export interface NotificationChannel {
  id: string;
  name: string;
  type: "smtp" | "webhook" | "telegram" | "feishu" | "dingtalk" | "wecom";
  events: string[];
  enabled: boolean;
  public_config: Record<string, unknown>;
  configured_fields: string[];
  last_delivery_status?: string;
  last_delivery_at?: number;
  last_delivery_error?: string;
  created_at: number;
  updated_at: number;
}

export interface AuditLog {
  id: string;
  actor_id: string | null;
  action: string;
  resource_type: string;
  resource_id: string | null;
  ip: string | null;
  user_agent: string | null;
  request_id: string | null;
  result: "ok" | "denied" | "error";
  detail_json: string | null;
  created_at: number;
}

export interface CertificateDetail {
  certificate: Certificate;
  ca_account: CAAccount | null;
  dns_credential: DNSCredential | null;
  current_revision: Revision | null;
  deployments: Deployment[];
  jobs: Job[];
}

export interface Dashboard {
  certificates: { total: number; expiring_30d: number; failed: number; active: number };
  jobs: Record<string, number>;
  recent_jobs: Job[];
}

export type WorkflowStepType = "apply" | "deploy" | "notify" | "verify" | "wait";

export interface WorkflowStep {
  id: string;
  type: WorkflowStepType;
  name?: string;
  config?: Record<string, unknown>;
  when?: "success" | "failure" | "always";
  parallel_group?: string;
  continue_on_error?: boolean;
}

export interface WorkflowNode {
  id: string;
  type: WorkflowStepType;
  name?: string;
  config?: Record<string, unknown>;
  position?: { x: number; y: number };
  continue_on_error?: boolean;
}

export interface WorkflowEdge {
  id: string;
  from: string;
  to: string;
  condition?: "success" | "failure" | "always";
}

export interface WorkflowDefinition {
  version: number;
  steps?: WorkflowStep[];
  nodes?: WorkflowNode[];
  edges?: WorkflowEdge[];
  allow_multiple_starts?: boolean;
}

export interface Workflow {
  id: string;
  name: string;
  description: string;
  definition: WorkflowDefinition;
  enabled: boolean;
  trigger_type: "manual" | "schedule";
  schedule: string;
  created_at: number;
  updated_at: number;
  last_run?: WorkflowRun | null;
}

export interface WorkflowRun {
  id: string;
  workflow_id: string;
  status: "queued" | "running" | "succeeded" | "failed" | "partial" | "canceled";
  trigger_type: string;
  error: string | null;
  started_at: number | null;
  finished_at: number | null;
  created_at: number;
}

export interface WorkflowStepRun {
  id: string;
  run_id: string;
  step_id: string;
  type: WorkflowStepType;
  name: string;
  status: "pending" | "running" | "succeeded" | "failed" | "skipped" | "canceled";
  attempts: number;
  job_ids: string[];
  output: Record<string, unknown>;
  error: string | null;
  started_at: number | null;
  finished_at: number | null;
  available_at?: number;
}

export interface WorkflowRunDetail {
  run: WorkflowRun;
  steps: WorkflowStepRun[];
  log_url: string;
}

export interface SystemInfo {
  version: string;
  acme_version: string;
  setup_required: boolean;
  data_dir: string;
  disk_free_bytes: number;
  server_time: number;
  fake_acme: boolean;
  instance_name: string;
  default_ca_id: string;
  default_theme: "light" | "dark" | string;
  renewal_notice_days: number;
}

export interface Problem {
  type: string;
  title: string;
  status: number;
  code: string;
  detail?: string;
  request_id?: string;
  fields?: Record<string, string>;
}

export interface Page<T> {
  items: T[];
  next_cursor?: number | string | null;
}

export interface CAInput {
  name: string;
  directory_url: string;
  email: string;
  eab_kid: string;
  eab_hmac_key: string;
}

export interface DNSInput {
  name: string;
  provider: string;
  config: Record<string, string>;
}

export interface CertInput {
  name: string;
  primary_domain: string;
  san: string[];
  key_type: string;
  ca_account_id: string;
  challenge_type: ChallengeType;
  dns_credential_id?: string;
  webroot?: string;
  options?: CertificateOptions;
  auto_renew: boolean;
  auto_deploy?: boolean;
  version?: number;
}
