import type { Workflow, WorkflowStep, WorkflowStepType } from "@/api/types";

export type GraphNode = {
  id: string;
  type: WorkflowStepType;
  name: string;
  config: Record<string, unknown>;
  position: { x: number; y: number };
  continue_on_error: boolean;
};

export type GraphEdge = {
  id: string;
  from: string;
  to: string;
  condition: "success" | "failure" | "always";
};

export type EditForm = {
  name: string;
  description: string;
  enabled: boolean;
  trigger_type: "manual" | "schedule";
  schedule: string;
  nodes: GraphNode[];
  edges: GraphEdge[];
  allow_multiple_starts: boolean;
};

export const stepTypes: WorkflowStepType[] = ["apply", "deploy", "notify", "verify", "wait"];

export function newNode(type: WorkflowStepType, position?: { x: number; y: number }): GraphNode {
  return {
    id: `${type}-${Date.now()}-${Math.random().toString(36).slice(2, 6)}`,
    type,
    name: "",
    config: defaultConfig(type),
    position: position ?? { x: 80, y: 80 },
    continue_on_error: false,
  };
}

export function defaultConfig(type: WorkflowStepType): Record<string, unknown> {
  switch (type) {
    case "apply":
      return {
        certificate_id: "",
        primary_domain: "",
        key_type: "ec-256",
        ca_account_id: "",
        challenge_type: "dns-01",
        dns_credential_id: "",
        webroot: "",
        mode: "auto",
        force: false,
      };
    case "deploy":
      return { certificate_id: "", revision_id: "" };
    case "notify":
      return { event: "workflow.notify", title: "", body: "" };
    case "verify":
      return { certificate_id: "", min_days: 7 };
    case "wait":
      return { seconds: 5 };
  }
}

export function fromWorkflow(w: Workflow): EditForm {
  const definition = w.definition;
  const legacy = !definition?.nodes?.length;
  let nodes: GraphNode[] = [];
  let edges: GraphEdge[] = [];
  if (definition?.nodes?.length) {
    nodes = definition.nodes.map((n, i) => ({
      id: n.id,
      type: n.type,
      name: n.name ?? "",
      config: { ...(n.config ?? {}) },
      position: n.position ?? { x: 80 + (i % 4) * 260, y: 80 + Math.floor(i / 4) * 160 },
      continue_on_error: Boolean(n.continue_on_error),
    }));
    edges = (definition.edges ?? []).map((e, i) => ({
      id: e.id || `edge-${i + 1}`,
      from: e.from,
      to: e.to,
      condition: e.condition ?? "success",
    }));
  } else {
    const converted = stepsToGraph(definition?.steps ?? []);
    nodes = converted.nodes;
    edges = converted.edges;
  }
  const incoming = new Set(edges.map((e) => e.to));
  const rootCount = nodes.filter((n) => !incoming.has(n.id)).length;
  return {
    name: w.name,
    description: w.description,
    enabled: w.enabled,
    trigger_type: w.trigger_type,
    schedule: w.schedule,
    nodes,
    edges,
    // A legacy first parallel group legitimately has multiple roots. Keep that
    // conversion working, but do not silently opt existing v2 graphs into
    // multiple starts: their orphaned nodes should surface as a validation
    // error so the user can reconnect them.
    allow_multiple_starts: Boolean(definition?.allow_multiple_starts) || (legacy && rootCount > 1),
  };
}

export function stepsToGraph(steps: WorkflowStep[]): { nodes: GraphNode[]; edges: GraphEdge[] } {
  const nodes: GraphNode[] = [];
  const edges: GraphEdge[] = [];
  let previous: string[] = [];
  let edgeSeq = 0;
  let x = 80;
  for (let i = 0; i < steps.length; ) {
    const group = steps[i].parallel_group ?? "";
    const batch: WorkflowStep[] = [];
    if (group === "") {
      // Ordinary steps are sequential. Only an explicit non-empty
      // parallel_group starts a batch.
      batch.push(steps[i]);
      i++;
    } else {
      while (i < steps.length && (steps[i].parallel_group ?? "") === group) {
        batch.push(steps[i]);
        i++;
      }
    }
    const batchIDs: string[] = [];
    batch.forEach((step, row) => {
      const id = step.id || `node-${nodes.length + 1}`;
      batchIDs.push(id);
      nodes.push({
        id,
        type: step.type,
        name: step.name ?? "",
        config: { ...(step.config ?? {}) },
        position: { x, y: 80 + row * 150 },
        continue_on_error: Boolean(step.continue_on_error),
      });
    });
    for (const from of previous) {
      for (const [row, step] of batch.entries()) {
        edges.push({
          id: `edge-${++edgeSeq}`,
          from,
          to: batchIDs[row],
          condition: step.when === "failure" ? "failure" : step.when === "always" ? "always" : "success",
        });
      }
    }
    previous = batchIDs;
    x += 260;
  }
  return { nodes, edges };
}

export type FormIssue = { key: string; nodeID?: string; params?: Record<string, unknown> };

export function validateEditForm(form: EditForm): FormIssue[] {
  const issues: FormIssue[] = [];
  if (!form.name.trim()) issues.push({ key: "workflow.validation.name" });
  if (!form.nodes.length) issues.push({ key: "workflow.validation.noNodes" });
  if (form.trigger_type === "schedule" && !form.schedule.trim()) {
    issues.push({ key: "workflow.validation.schedule" });
  }

  const ids = new Set(form.nodes.map((n) => n.id));
  const seenEdges = new Set<string>();
  const outgoing = new Map<string, string[]>();
  form.nodes.forEach((n) => outgoing.set(n.id, []));
  for (const e of form.edges) {
    if (!ids.has(e.from) || !ids.has(e.to) || e.from === e.to) {
      issues.push({ key: "workflow.validation.edge", nodeID: e.from });
      continue;
    }
    const pair = `${e.from}>${e.to}`;
    if (seenEdges.has(pair)) {
      issues.push({ key: "workflow.validation.duplicateEdge", nodeID: e.from });
    }
    seenEdges.add(pair);
    outgoing.get(e.from)?.push(e.to);
  }

  const state = new Map<string, number>();
  const visit = (id: string): boolean => {
    const s = state.get(id) ?? 0;
    if (s === 1) return true;
    if (s === 2) return false;
    state.set(id, 1);
    for (const next of outgoing.get(id) ?? []) {
      if (visit(next)) return true;
    }
    state.set(id, 2);
    return false;
  };
  for (const n of form.nodes) {
    if (visit(n.id)) {
      issues.push({ key: "workflow.validation.cycle", nodeID: n.id });
      break;
    }
  }

  const incoming = new Set(form.edges.map((e) => e.to));
  const roots = form.nodes.filter((n) => !incoming.has(n.id));
  if (roots.length > 1 && !form.allow_multiple_starts) {
    issues.push({
      key: "workflow.validation.multipleStarts",
      nodeID: roots[1]?.id,
      params: { count: roots.length },
    });
  }

  for (const n of form.nodes) {
    const cfg = n.config ?? {};
    const certID = String(cfg.certificate_id ?? "").trim();
    switch (n.type) {
      case "apply":
        if (!certID && !String(cfg.primary_domain ?? "").trim()) {
          issues.push({ key: "workflow.validation.apply", nodeID: n.id });
          break;
        }
        if (!certID) {
          if (!String(cfg.key_type ?? "").trim() || !String(cfg.ca_account_id ?? "").trim() || !String(cfg.challenge_type ?? "").trim()) {
            issues.push({ key: "workflow.validation.applyInline", nodeID: n.id });
          }
          const challenge = String(cfg.challenge_type ?? "");
          if (challenge === "dns-01" && !String(cfg.dns_credential_id ?? "").trim()) {
            issues.push({ key: "workflow.validation.dnsCredential", nodeID: n.id });
          }
          if (challenge === "http-01-webroot" && !String(cfg.webroot ?? "").trim()) {
            issues.push({ key: "workflow.validation.webroot", nodeID: n.id });
          }
        }
        break;
      case "notify":
        if (!String(cfg.title ?? "").trim() && !String(cfg.body ?? "").trim()) {
          issues.push({ key: "workflow.validation.notify", nodeID: n.id });
        }
        break;
      case "wait": {
        const seconds = Number(cfg.seconds ?? 0);
        if (!Number.isFinite(seconds) || seconds < 1 || seconds > 86400) {
          issues.push({ key: "workflow.validation.wait", nodeID: n.id });
        }
        break;
      }
      case "verify": {
        const minDays = Number(cfg.min_days ?? 0);
        if (!Number.isFinite(minDays) || minDays < 0 || minDays > 365) {
          issues.push({ key: "workflow.validation.verify", nodeID: n.id });
        }
        break;
      }
    }
  }
  return issues;
}

export function nodeSummary(n: GraphNode, t: (key: string) => string): string {
  switch (n.type) {
    case "apply":
      return String(n.config.certificate_id || n.config.primary_domain || t("workflow.inlineCertificate"));
    case "deploy":
      return String(n.config.certificate_id || t("workflow.inherit"));
    case "notify":
      return String(n.config.title || n.config.event || "notify");
    case "verify":
      return `${t("workflow.minDays")}: ${n.config.min_days ?? 7}`;
    case "wait":
      return `${n.config.seconds ?? 5}s`;
  }
}

export function emptyForm(): EditForm {
  const apply = newNode("apply", { x: 80, y: 120 });
  const deploy = newNode("deploy", { x: 380, y: 120 });
  const notify = newNode("notify", { x: 680, y: 120 });
  // Give the scaffold a valid notification body so only the apply node needs
  // configuration before the workflow can be saved.
  notify.config = { ...notify.config, title: "Workflow completed", body: "Run {{run_id}} finished." };
  return {
    name: "",
    description: "",
    enabled: true,
    trigger_type: "manual",
    schedule: "daily 03:00",
    nodes: [apply, deploy, notify],
    edges: [
      { id: `edge-${Date.now()}-1`, from: apply.id, to: deploy.id, condition: "success" },
      { id: `edge-${Date.now()}-2`, from: deploy.id, to: notify.id, condition: "success" },
    ],
    allow_multiple_starts: false,
  };
}

export function duration(start: number | null, end: number | null): string {
  if (!start) return "—";
  const s = Math.max(0, (end ?? Math.floor(Date.now() / 1000)) - start);
  if (s < 60) return `${s}s`;
  return `${Math.floor(s / 60)}m ${s % 60}s`;
}
