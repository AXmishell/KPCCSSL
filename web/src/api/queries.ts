import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";
import { errorMessage, request } from "./client";
import type {
  AuditLog,
  CAAccount,
  CAPreset,
  Certificate,
  CertificateDetail,
  CertEvent,
  Dashboard,
  Deployment,
  DNSCredential,
  DNSProvider,
  Job,
  NotificationChannel,
  Page,
  Revision,
  SystemInfo,
  User,
  Workflow,
  WorkflowRun,
  WorkflowRunDetail,
} from "./types";

export const qk = {
  ca: ["ca-accounts"] as const,
  dnsProviders: ["dns-providers"] as const,
  dns: ["dns-credentials"] as const,
  certs: (params: string) => ["certificates", params] as const,
  cert: (id: string) => ["certificate", id] as const,
  revisions: (id: string) => ["revisions", id] as const,
  events: (id: string) => ["events", id] as const,
  jobs: (params: string) => ["jobs", params] as const,
  job: (id: string) => ["job", id] as const,
  deployments: (certId?: string) => ["deployments", certId ?? "all"] as const,
  notifications: ["notifications"] as const,
  audit: ["audit"] as const,
  settings: ["settings"] as const,
  info: ["system-info"] as const,
  dashboard: ["dashboard"] as const,
  workflows: ["workflows"] as const,
  workflow: (id: string) => ["workflow", id] as const,
  workflowRuns: (id: string) => ["workflow-runs", id] as const,
  workflowRun: (id: string) => ["workflow-run", id] as const,
};

export function useCA() {
  return useQuery({
    queryKey: qk.ca,
    queryFn: () => request<{ items: CAAccount[]; presets: CAPreset[] }>("/ca-accounts"),
    refetchInterval: (query) => (query.state.data?.items.some((c) => c.status === "new") ? 2000 : false),
  });
}

export function useDNSProviders() {
  return useQuery({
    queryKey: qk.dnsProviders,
    queryFn: () => request<{ items: DNSProvider[] }>("/dns-providers"),
    staleTime: Infinity,
  });
}

export function useDNS() {
  return useQuery({ queryKey: qk.dns, queryFn: () => request<{ items: DNSCredential[] }>("/dns-credentials") });
}

export function useCertificates(params: { status?: string; q?: string; limit?: number }) {
  const search = new URLSearchParams();
  if (params.status) search.set("status", params.status);
  if (params.q) search.set("q", params.q);
  search.set("limit", String(params.limit ?? 100));
  const key = search.toString();
  return useQuery({
    queryKey: qk.certs(key),
    queryFn: () => request<Page<Certificate>>(`/certificates?${key}`),
  });
}

export function useCertificate(id?: string) {
  return useQuery({
    queryKey: qk.cert(id ?? ""),
    queryFn: () => request<CertificateDetail>(`/certificates/${id}`),
    enabled: Boolean(id),
    refetchInterval: (query) => {
      const status = query.state.data?.certificate.status;
      return status && ["queued", "issuing", "renewing"].includes(status) ? 2000 : false;
    },
  });
}

export function useRevisions(id?: string) {
  return useQuery({
    queryKey: qk.revisions(id ?? ""),
    queryFn: () => request<{ items: Revision[] }>(`/certificates/${id}/revisions`),
    enabled: Boolean(id),
  });
}

export function useCertEvents(id?: string) {
  return useQuery({
    queryKey: qk.events(id ?? ""),
    queryFn: () => request<{ items: CertEvent[] }>(`/certificates/${id}/events`),
    enabled: Boolean(id),
  });
}

export function useJobs(params: { status?: string; certificate_id?: string } = {}) {
  const search = new URLSearchParams();
  if (params.status) search.set("status", params.status);
  if (params.certificate_id) search.set("certificate_id", params.certificate_id);
  search.set("limit", "100");
  const key = search.toString();
  return useQuery({
    queryKey: qk.jobs(key),
    queryFn: () => request<{ items: Job[] }>(`/jobs?${key}`),
    refetchInterval: 3000,
  });
}

export function useJob(id?: string) {
  return useQuery({
    queryKey: qk.job(id ?? ""),
    queryFn: () => request<Job>(`/jobs/${id}`),
    enabled: Boolean(id),
    refetchInterval: (query) => {
      const status = query.state.data?.status;
      return status && ["queued", "running"].includes(status) ? 1500 : false;
    },
  });
}

export function useDeployments(certId?: string) {
  const search = certId ? `?certificate_id=${certId}` : "";
  return useQuery({
    queryKey: qk.deployments(certId),
    queryFn: () => request<{ items: Deployment[] }>(`/deployments${search}`),
  });
}

export function useNotifications() {
  return useQuery({
    queryKey: qk.notifications,
    queryFn: () => request<{ items: NotificationChannel[]; events: string[] }>("/notification-channels"),
  });
}

export function useAudit() {
  return useQuery({ queryKey: qk.audit, queryFn: () => request<Page<AuditLog>>("/audit-logs?limit=100") });
}

export function useSettings() {
  return useQuery({ queryKey: qk.settings, queryFn: () => request<Record<string, unknown>>("/settings") });
}

export function useInfo() {
  return useQuery({ queryKey: qk.info, queryFn: () => request<SystemInfo>("/system/info") });
}

export function useUsers() {
  return useQuery({ queryKey: ["users"], queryFn: () => request<{ items: User[] }>("/users") });
}

export function useWorkflows() {
  return useQuery({
    queryKey: qk.workflows,
    queryFn: () => request<{ items: Workflow[] }>("/workflows"),
    // Poll quickly only while a run is active; otherwise keep the list fresh
    // at a low rate so scheduled runs still appear.
    refetchInterval: (query) =>
      query.state.data?.items.some((w) => w.last_run && ["queued", "running"].includes(w.last_run.status))
        ? 3000
        : 15000,
    refetchIntervalInBackground: false,
  });
}

export function useWorkflow(id?: string) {
  return useQuery({
    queryKey: qk.workflow(id ?? ""),
    queryFn: () => request<Workflow>(`/workflows/${id}`),
    enabled: Boolean(id),
  });
}

export function useWorkflowRuns(id?: string) {
  return useQuery({
    queryKey: qk.workflowRuns(id ?? ""),
    queryFn: () => request<{ items: WorkflowRun[] }>(`/workflows/${id}/runs?limit=50`),
    enabled: Boolean(id),
    refetchInterval: (query) =>
      query.state.data?.items.some((r) => ["queued", "running"].includes(r.status)) ? 2000 : 10000,
    refetchIntervalInBackground: false,
  });
}

export function useWorkflowRun(id?: string) {
  return useQuery({
    queryKey: qk.workflowRun(id ?? ""),
    queryFn: () => request<WorkflowRunDetail>(`/workflow-runs/${id}`),
    enabled: Boolean(id),
    refetchInterval: (query) => {
      const status = query.state.data?.run.status;
      return status && ["queued", "running"].includes(status) ? 1500 : false;
    },
  });
}

export function useDashboard() {
  return useQuery({
    queryKey: qk.dashboard,
    queryFn: () => request<Dashboard>("/dashboard"),
    refetchInterval: 5000,
  });
}

export function useInvalidateAll() {
  const qc = useQueryClient();
  return () => qc.invalidateQueries();
}

/** useAction wraps a mutation with toast feedback and query invalidation. */
export function useAction<TArgs, TResult>(
  fn: (args: TArgs) => Promise<TResult>,
  opts: { success?: string; onDone?: (result: TResult) => void } = {},
) {
  const qc = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: fn,
    onSuccess: (result) => {
      if (opts.success) toast.success(opts.success);
      void qc.invalidateQueries();
      opts.onDone?.(result);
    },
    onError: (err) => toast.error(errorMessage(err) || t("errors.generic")),
  });
}
