import * as React from "react";
import { useTranslation } from "react-i18next";
import { useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { ArrowDown, ArrowUp, GitBranch, History, Pencil, Play, Plus, Trash2 } from "lucide-react";
import { useAuth } from "@/auth";
import { errorMessage, request } from "@/api/client";
import { qk, useCA, useCertificates, useDNS, useWorkflowRun, useWorkflowRuns, useWorkflows } from "@/api/queries";
import type { Workflow, WorkflowDefinition, WorkflowRun, WorkflowStep, WorkflowStepType } from "@/api/types";
import { Badge, statusVariant } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Field, Input, Select, Textarea } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { EmptyState, ErrorBox, PageHeader, Spinner, Table, Td, Th } from "@/components/ui/misc";
import { formatDateTime, fromNow } from "@/lib/format";

type GraphNode = {
  id: string;
  type: WorkflowStepType;
  name: string;
  config: Record<string, unknown>;
  position: { x: number; y: number };
  continue_on_error: boolean;
};

type GraphEdge = {
  id: string;
  from: string;
  to: string;
  condition: "success" | "failure" | "always";
};

type EditForm = {
  name: string;
  description: string;
  enabled: boolean;
  trigger_type: "manual" | "schedule";
  schedule: string;
  nodes: GraphNode[];
  edges: GraphEdge[];
};

const stepTypes: WorkflowStepType[] = ["apply", "deploy", "notify", "verify", "wait"];

function newNode(type: WorkflowStepType, position?: { x: number; y: number }): GraphNode {
  return {
    id: `${type}-${Date.now()}-${Math.random().toString(36).slice(2, 6)}`,
    type,
    name: "",
    config: defaultConfig(type),
    position: position ?? { x: 80, y: 80 },
    continue_on_error: false,
  };
}

function defaultConfig(type: WorkflowStepType): Record<string, unknown> {
  switch (type) {
    case "apply":
      return { certificate_id: "", mode: "auto", force: false };
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

function fromWorkflow(w: Workflow): EditForm {
  const definition = w.definition;
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
  return {
    name: w.name,
    description: w.description,
    enabled: w.enabled,
    trigger_type: w.trigger_type,
    schedule: w.schedule,
    nodes,
    edges,
  };
}

function stepsToGraph(steps: WorkflowStep[]): { nodes: GraphNode[]; edges: GraphEdge[] } {
  const nodes: GraphNode[] = [];
  const edges: GraphEdge[] = [];
  let previous: string[] = [];
  let edgeSeq = 0;
  let x = 80;
  for (let i = 0; i < steps.length; ) {
    const group = steps[i].parallel_group ?? "";
    const batch: WorkflowStep[] = [];
    while (i < steps.length && (steps[i].parallel_group ?? "") === group) {
      batch.push(steps[i]);
      i++;
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

export function WorkflowsPage() {
  const { t, i18n } = useTranslation();
  const { user } = useAuth();
  const qc = useQueryClient();
  const { data, isLoading, error } = useWorkflows();
  const [editOpen, setEditOpen] = React.useState(false);
  const [editing, setEditing] = React.useState<Workflow | null>(null);
  const [runsFor, setRunsFor] = React.useState<Workflow | null>(null);
  const isAdmin = user?.role === "admin";
  const canRun = user?.role === "admin" || user?.role === "operator";

  const invalidate = () => void qc.invalidateQueries({ queryKey: qk.workflows });

  const run = async (w: Workflow) => {
    try {
      await request(`/workflows/${w.id}/run`, { method: "POST", body: {} });
      toast.success(t("workflow.queued"));
      invalidate();
    } catch (err) {
      toast.error(errorMessage(err));
    }
  };

  const remove = async (w: Workflow) => {
    if (!window.confirm(t("workflow.deleteConfirm", { name: w.name }))) return;
    try {
      await request(`/workflows/${w.id}`, { method: "DELETE" });
      toast.success(t("workflow.deleted"));
      invalidate();
    } catch (err) {
      toast.error(errorMessage(err));
    }
  };

  if (isLoading) {
    return <div className="flex justify-center py-20"><Spinner className="h-6 w-6" /></div>;
  }
  if (error) return <ErrorBox message={errorMessage(error)} />;

  return (
    <div>
      <PageHeader
        title={t("workflow.title")}
        subtitle={t("workflow.subtitle")}
        actions={isAdmin && (
          <Button onClick={() => { setEditing(null); setEditOpen(true); }}>
            <Plus className="h-4 w-4" /> {t("workflow.add")}
          </Button>
        )}
      />

      {!data?.items.length ? (
        <EmptyState
          description={t("workflow.empty")}
          action={isAdmin ? <Button onClick={() => { setEditing(null); setEditOpen(true); }}>{t("workflow.add")}</Button> : undefined}
        />
      ) : (
        <Table>
          <thead>
            <tr>
              <Th>{t("common.name")}</Th>
              <Th>{t("workflow.trigger")}</Th>
              <Th>{t("workflow.steps")}</Th>
              <Th>{t("common.status")}</Th>
              <Th>{t("workflow.lastRun")}</Th>
              <Th />
            </tr>
          </thead>
          <tbody>
            {data.items.map((w) => (
              <tr key={w.id}>
                <Td>
                  <div className="font-medium">{w.name}</div>
                  <div className="max-w-[24rem] truncate text-xs text-muted-foreground">{w.description || w.id}</div>
                </Td>
                <Td className="text-sm text-muted-foreground">
                  {w.trigger_type === "schedule" ? w.schedule : t("workflow.manual")}
                </Td>
                <Td className="tabular-nums">{w.definition?.steps?.length ?? 0}</Td>
                <Td>
                  <Badge variant={w.enabled ? "success" : "muted"}>{w.enabled ? t("common.enabled") : t("common.disabled")}</Badge>
                </Td>
                <Td>
                  {w.last_run ? (
                    <div className="flex items-center gap-2">
                      <Badge variant={statusVariant(w.last_run.status)}>{t(`status.${w.last_run.status}`)}</Badge>
                      <span className="text-xs text-muted-foreground">{fromNow(w.last_run.created_at, i18n.language)}</span>
                    </div>
                  ) : <span className="text-muted-foreground">—</span>}
                </Td>
                <Td className="text-right">
                  <div className="flex flex-wrap justify-end gap-1">
                    {canRun && (
                      <Button variant="outline" size="sm" onClick={() => void run(w)}>
                        <Play className="h-3.5 w-3.5" /> {t("workflow.run")}
                      </Button>
                    )}
                    <Button variant="ghost" size="sm" onClick={() => setRunsFor(w)}>
                      <History className="h-3.5 w-3.5" /> {t("workflow.runs")}
                    </Button>
                    {isAdmin && (
                      <>
                        <Button variant="ghost" size="sm" onClick={() => { setEditing(w); setEditOpen(true); }}>
                          <Pencil className="h-3.5 w-3.5" />
                        </Button>
                        <Button variant="ghost" size="icon" onClick={() => void remove(w)}>
                          <Trash2 className="h-4 w-4 text-destructive" />
                        </Button>
                      </>
                    )}
                  </div>
                </Td>
              </tr>
            ))}
          </tbody>
        </Table>
      )}

      <WorkflowEditor open={editOpen} onOpenChange={setEditOpen} workflow={editing} onSaved={invalidate} />
      <RunsDialog workflow={runsFor} canRun={canRun} onOpenChange={(open) => !open && setRunsFor(null)} />
    </div>
  );
}

function WorkflowEditor({
  open,
  onOpenChange,
  workflow,
  onSaved,
}: {
  open: boolean;
  onOpenChange: (v: boolean) => void;
  workflow: Workflow | null;
  onSaved: () => void;
}) {
  const { t } = useTranslation();
  const { data: certData } = useCertificates({});
  const { data: caData } = useCA();
  const { data: dnsData } = useDNS();
  const [form, setForm] = React.useState<EditForm>(() => emptyForm());
  const [error, setError] = React.useState("");
  const [busy, setBusy] = React.useState(false);

  React.useEffect(() => {
    if (!open) return;
    setError("");
    setForm(workflow ? fromWorkflow(workflow) : emptyForm());
  }, [open, workflow]);

  const certs = certData?.items ?? [];

  const save = async () => {
    setBusy(true);
    setError("");
    const definition: WorkflowDefinition = {
      version: 2,
      nodes: form.nodes.map((n) => ({
        id: n.id,
        type: n.type,
        name: n.name,
        config: n.config,
        position: n.position,
        continue_on_error: n.continue_on_error,
      })),
      edges: form.edges.map((e) => ({ id: e.id, from: e.from, to: e.to, condition: e.condition })),
    };
    try {
      if (workflow) {
        await request(`/workflows/${workflow.id}`, {
          method: "PATCH",
          body: {
            name: form.name,
            description: form.description,
            enabled: form.enabled,
            trigger_type: form.trigger_type,
            schedule: form.schedule,
            definition,
          },
        });
      } else {
        await request("/workflows", {
          method: "POST",
          body: {
            name: form.name,
            description: form.description,
            enabled: form.enabled,
            trigger_type: form.trigger_type,
            schedule: form.schedule,
            definition,
          },
        });
      }
      toast.success(t("workflow.saved"));
      onSaved();
      onOpenChange(false);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[92vh] max-w-7xl overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{workflow ? t("workflow.edit") : t("workflow.add")}</DialogTitle>
          <DialogDescription>{t("workflow.editorHint")}</DialogDescription>
        </DialogHeader>
        {error && <ErrorBox message={error} />}
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label={t("common.name")}>
            <Input value={form.name} onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))} />
          </Field>
          <Field label={t("workflow.trigger")}>
            <Select value={form.trigger_type} onChange={(e) => setForm((f) => ({ ...f, trigger_type: e.target.value as EditForm["trigger_type"] }))}>
              <option value="manual">{t("workflow.manual")}</option>
              <option value="schedule">{t("workflow.schedule")}</option>
            </Select>
          </Field>
          <Field label={t("common.description")}>
            <Input value={form.description} onChange={(e) => setForm((f) => ({ ...f, description: e.target.value }))} />
          </Field>
          <div className="flex items-end gap-4">
            <Field label={t("workflow.enabled")}>
              <Switch checked={form.enabled} onCheckedChange={(v) => setForm((f) => ({ ...f, enabled: v }))} />
            </Field>
            {form.trigger_type === "schedule" && (
              <Field label={t("workflow.schedule")} hint={t("workflow.scheduleHint")}>
                <Input value={form.schedule} onChange={(e) => setForm((f) => ({ ...f, schedule: e.target.value }))} placeholder="daily 03:00" />
              </Field>
            )}
          </div>
        </div>

        <div className="mt-5">
          <GraphEditor
            form={form}
            setForm={setForm}
            certs={certs}
            caList={caData?.items ?? []}
            dnsList={dnsData?.items ?? []}
          />
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>{t("common.cancel")}</Button>
          <Button onClick={() => void save()} disabled={busy || !form.name || !form.nodes.length}>{t("common.save")}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

const NODE_W = 220;
const NODE_H = 96;

function GraphEditor({
  form,
  setForm,
  certs,
  caList,
  dnsList,
}: {
  form: EditForm;
  setForm: React.Dispatch<React.SetStateAction<EditForm>>;
  certs: Array<{ id: string; name: string; primary_domain: string }>;
  caList: Array<{ id: string; name: string; status: string }>;
  dnsList: Array<{ id: string; name: string; provider: string }>;
}) {
  const { t } = useTranslation();
  const canvasRef = React.useRef<HTMLDivElement>(null);
  const [selectedNode, setSelectedNode] = React.useState("");
  const [selectedEdge, setSelectedEdge] = React.useState("");
  const [pendingFrom, setPendingFrom] = React.useState("");
  const [connect, setConnect] = React.useState<{ from: string; x: number; y: number; startX: number; startY: number; moved: boolean } | null>(null);
  const drag = React.useRef<{ id: string; dx: number; dy: number } | null>(null);

  const width = Math.max(1600, ...form.nodes.map((n) => n.position.x + NODE_W + 360));
  const height = Math.max(720, ...form.nodes.map((n) => n.position.y + NODE_H + 240));
  const nodeByID = React.useMemo(() => new Map(form.nodes.map((n) => [n.id, n])), [form.nodes]);

  const toCanvas = (clientX: number, clientY: number) => {
    const el = canvasRef.current;
    if (!el) return { x: 0, y: 0 };
    const rect = el.getBoundingClientRect();
    return { x: clientX - rect.left + el.scrollLeft, y: clientY - rect.top + el.scrollTop };
  };

  React.useEffect(() => {
    if (!connect) return;
    const move = (e: PointerEvent) => {
      const p = toCanvas(e.clientX, e.clientY);
      setConnect((c) => c && { ...c, x: p.x, y: p.y, moved: c.moved || Math.hypot(p.x - c.startX, p.y - c.startY) > 4 });
    };
    const up = (e: PointerEvent) => {
      const target = document.elementFromPoint(e.clientX, e.clientY)?.closest("[data-input-node]") as HTMLElement | null;
      const to = target?.getAttribute("data-input-node") ?? "";
      if (to && to !== connect.from) {
        addEdge(connect.from, to);
      } else if (!connect.moved) {
        setPendingFrom(connect.from);
      }
      setConnect(null);
    };
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", up);
    return () => {
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", up);
    };
  }, [connect]);

  React.useEffect(() => {
    const move = (e: PointerEvent) => {
      if (!drag.current) return;
      const p = toCanvas(e.clientX, e.clientY);
      const d = drag.current;
      setForm((f) => ({
        ...f,
        nodes: f.nodes.map((n) => n.id === d.id ? { ...n, position: { x: Math.max(0, p.x - d.dx), y: Math.max(0, p.y - d.dy) } } : n),
      }));
    };
    const up = () => { drag.current = null; };
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", up);
    return () => {
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", up);
    };
  }, [setForm]);

  const addEdge = (from: string, to: string) => {
    if (!from || !to || from === to) return;
    setForm((f) => {
      if (f.edges.some((e) => e.from === from && e.to === to)) return f;
      return { ...f, edges: [...f.edges, { id: `edge-${Date.now()}-${Math.random().toString(36).slice(2, 5)}`, from, to, condition: "success" }] };
    });
    setPendingFrom("");
  };

  const updateNode = (id: string, patch: Partial<GraphNode>) => {
    setForm((f) => ({ ...f, nodes: f.nodes.map((n) => n.id === id ? { ...n, ...patch } : n) }));
  };
  const updateConfig = (id: string, key: string, value: unknown) => {
    setForm((f) => ({ ...f, nodes: f.nodes.map((n) => n.id === id ? { ...n, config: { ...n.config, [key]: value } } : n) }));
  };
  const removeNode = (id: string) => {
    setForm((f) => ({ ...f, nodes: f.nodes.filter((n) => n.id !== id), edges: f.edges.filter((e) => e.from !== id && e.to !== id) }));
    setSelectedNode("");
    setSelectedEdge("");
  };
  const removeEdge = (id: string) => {
    setForm((f) => ({ ...f, edges: f.edges.filter((e) => e.id !== id) }));
    setSelectedEdge("");
  };
  const addNode = (type: WorkflowStepType) => {
    const id = `${type}-${Date.now()}-${Math.random().toString(36).slice(2, 5)}`;
    const n = newNode(type, { x: 80 + (form.nodes.length % 4) * 270, y: 80 + Math.floor(form.nodes.length / 4) * 150 });
    n.id = id;
    setForm((f) => ({ ...f, nodes: [...f.nodes, n] }));
    setSelectedNode(id);
  };
  const autoLayout = () => {
    const indegree = new Map<string, number>();
    const levels = new Map<string, number>();
    const out = new Map<string, string[]>();
    form.nodes.forEach((n) => { indegree.set(n.id, 0); levels.set(n.id, 0); out.set(n.id, []); });
    form.edges.forEach((e) => {
      indegree.set(e.to, (indegree.get(e.to) ?? 0) + 1);
      out.get(e.from)?.push(e.to);
    });
    const queue = form.nodes.filter((n) => (indegree.get(n.id) ?? 0) === 0).map((n) => n.id);
    for (let i = 0; i < queue.length; i++) {
      const id = queue[i];
      for (const next of out.get(id) ?? []) {
        levels.set(next, Math.max(levels.get(next) ?? 0, (levels.get(id) ?? 0) + 1));
        indegree.set(next, (indegree.get(next) ?? 1) - 1);
        if ((indegree.get(next) ?? 0) === 0) queue.push(next);
      }
    }
    const rowByLevel = new Map<number, number>();
    const positioned = form.nodes.map((n) => {
      const level = levels.get(n.id) ?? 0;
      const row = rowByLevel.get(level) ?? 0;
      rowByLevel.set(level, row + 1);
      return { ...n, position: { x: 80 + level * 280, y: 80 + row * 150 } };
    });
    setForm((f) => ({ ...f, nodes: positioned }));
  };

  const selected = nodeByID.get(selectedNode);
  const selectedEdgeObj = form.edges.find((e) => e.id === selectedEdge);

  return (
    <div className="grid gap-3 lg:grid-cols-[minmax(0,1fr)_340px]">
      <div className="min-w-0">
        <div className="mb-2 flex flex-wrap items-center justify-between gap-2">
          <h3 className="text-sm font-semibold">{t("workflow.graph")}</h3>
          <div className="flex flex-wrap gap-1">
            {stepTypes.map((type) => (
              <Button key={type} size="sm" variant="outline" onClick={() => addNode(type)}>
                <Plus className="h-3 w-3" /> {t(`workflow.types.${type}`)}
              </Button>
            ))}
            <Button size="sm" variant="ghost" onClick={autoLayout}>{t("workflow.autoLayout")}</Button>
          </div>
        </div>
        <p className="mb-2 text-xs text-muted-foreground">{t("workflow.connectHint")}</p>
        <div
          ref={canvasRef}
          className="relative h-[560px] overflow-auto rounded-md border border-border bg-muted/10"
          style={{
            backgroundImage: "radial-gradient(circle, rgba(120,120,120,.28) 1px, transparent 1px)",
            backgroundSize: "20px 20px",
            cursor: connect ? "crosshair" : "default",
          }}
          onClick={() => { setSelectedNode(""); setSelectedEdge(""); setPendingFrom(""); }}
        >
          <div className="relative" style={{ width, height }}>
            <svg width={width} height={height} className="absolute inset-0">
              <defs>
                <marker id="arrow" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto">
                  <path d="M0,0 L8,4 L0,8 z" fill="currentColor" />
                </marker>
              </defs>
              {form.edges.map((e) => {
                const from = nodeByID.get(e.from);
                const to = nodeByID.get(e.to);
                if (!from || !to) return null;
                const x1 = from.position.x + NODE_W, y1 = from.position.y + NODE_H / 2;
                const x2 = to.position.x, y2 = to.position.y + NODE_H / 2;
                const dx = Math.max(70, Math.abs(x2 - x1) / 2);
                const path = `M ${x1} ${y1} C ${x1 + dx} ${y1}, ${x2 - dx} ${y2}, ${x2} ${y2}`;
                const color = e.condition === "failure" ? "#dc2626" : e.condition === "always" ? "#ca8a04" : "#2563eb";
                return (
                  <g key={e.id} className="cursor-pointer" onClick={(ev) => { ev.stopPropagation(); setSelectedEdge(e.id); setSelectedNode(""); }}>
                    <path d={path} fill="none" stroke="transparent" strokeWidth={14} />
                    <path d={path} fill="none" stroke={selectedEdge === e.id ? color : "currentColor"} strokeWidth={selectedEdge === e.id ? 2.5 : 1.6} markerEnd="url(#arrow)" className="text-muted-foreground/60" style={{ color: selectedEdge === e.id ? color : undefined }} />
                    <text x={(x1 + x2) / 2} y={(y1 + y2) / 2 - 6} textAnchor="middle" fontSize="10" fill={color}>{e.condition}</text>
                  </g>
                );
              })}
              {connect && (
                <path
                  d={`M ${(nodeByID.get(connect.from)?.position.x ?? 0) + NODE_W} ${(nodeByID.get(connect.from)?.position.y ?? 0) + NODE_H / 2} C ${(nodeByID.get(connect.from)?.position.x ?? 0) + NODE_W + 80} ${(nodeByID.get(connect.from)?.position.y ?? 0) + NODE_H / 2}, ${connect.x - 80} ${connect.y}, ${connect.x} ${connect.y}`}
                  fill="none" stroke="#2563eb" strokeDasharray="5 4" strokeWidth={2}
                />
              )}
            </svg>

            {form.nodes.map((n) => (
              <div
                key={n.id}
                className={`absolute select-none rounded-md border bg-card shadow-sm transition-shadow ${selectedNode === n.id ? "border-primary ring-2 ring-primary/20" : "border-border"}`}
                style={{ left: n.position.x, top: n.position.y, width: NODE_W, height: NODE_H }}
                onClick={(e) => { e.stopPropagation(); setSelectedNode(n.id); setSelectedEdge(""); }}
                onPointerDown={(e) => {
                  if ((e.target as HTMLElement).closest("[data-port]")) return;
                  const p = toCanvas(e.clientX, e.clientY);
                  drag.current = { id: n.id, dx: p.x - n.position.x, dy: p.y - n.position.y };
                }}
              >
                <div className="flex items-center justify-between rounded-t-md border-b border-border px-3 py-2">
                  <div className="flex items-center gap-2 text-xs font-semibold">
                    <GitBranch className="h-3.5 w-3.5 text-primary" />
                    {t(`workflow.types.${n.type}`)}
                  </div>
                  <span className="max-w-[90px] truncate text-[10px] text-muted-foreground">{n.name || n.id}</span>
                </div>
                <div className="px-3 py-2 text-[11px] text-muted-foreground">
                  {nodeSummary(n, t)}
                </div>
                <button
                  type="button"
                  data-port
                  data-input-node={n.id}
                  aria-label={t("workflow.inputPort")}
                  className={`absolute -left-2 top-1/2 h-4 w-4 -translate-y-1/2 rounded-full border-2 bg-background ${pendingFrom && pendingFrom !== n.id ? "border-primary" : "border-muted-foreground/70"}`}
                  onClick={(e) => { e.stopPropagation(); if (pendingFrom && pendingFrom !== n.id) addEdge(pendingFrom, n.id); }}
                />
                <button
                  type="button"
                  data-port
                  aria-label={t("workflow.outputPort")}
                  className="absolute -right-2 top-1/2 h-4 w-4 -translate-y-1/2 rounded-full border-2 border-primary bg-background"
                  onPointerDown={(e) => {
                    e.stopPropagation();
                    const p = toCanvas(e.clientX, e.clientY);
                    setConnect({ from: n.id, x: p.x, y: p.y, startX: p.x, startY: p.y, moved: false });
                  }}
                  onClick={(e) => e.stopPropagation()}
                />
              </div>
            ))}
            {!form.nodes.length && (
              <div className="absolute inset-0 flex items-center justify-center text-sm text-muted-foreground">
                {t("workflow.noNodes")}
              </div>
            )}
          </div>
        </div>
      </div>

      <div className="min-h-0">
        {selected ? (
          <StepEditor
            key={selected.id}
            step={{ id: selected.id, type: selected.type, name: selected.name, config: selected.config, continue_on_error: selected.continue_on_error }}
            index={0}
            total={1}
            graphMode
            certs={certs}
            caList={caList}
            dnsList={dnsList}
            onChange={(patch) => updateNode(selected.id, patch as Partial<GraphNode>)}
            onConfig={(key, value) => updateConfig(selected.id, key, value)}
            onMove={() => {}}
            onDelete={() => removeNode(selected.id)}
          />
        ) : selectedEdgeObj ? (
          <Card>
            <CardContent className="space-y-3 p-4">
              <h3 className="text-sm font-semibold">{t("workflow.edgeCondition")}</h3>
              <Field label={t("workflow.condition")}>
                <Select
                  value={selectedEdgeObj.condition}
                  onChange={(e) => setForm((f) => ({ ...f, edges: f.edges.map((edge) => edge.id === selectedEdgeObj.id ? { ...edge, condition: e.target.value as GraphEdge["condition"] } : edge) }))}
                >
                  <option value="success">{t("workflow.whenSuccess")}</option>
                  <option value="failure">{t("workflow.whenFailure")}</option>
                  <option value="always">{t("workflow.whenAlways")}</option>
                </Select>
              </Field>
              <Button variant="destructive" size="sm" onClick={() => removeEdge(selectedEdgeObj.id)}>
                <Trash2 className="h-3.5 w-3.5" /> {t("workflow.deleteEdge")}
              </Button>
            </CardContent>
          </Card>
        ) : (
          <div className="rounded-md border border-dashed border-border p-6 text-sm text-muted-foreground">
            {t("workflow.selectNode")}
          </div>
        )}
      </div>
    </div>
  );
}

function nodeSummary(n: GraphNode, t: (key: string) => string): string {
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

function StepEditor({
  step,
  index,
  total,
  graphMode = false,
  certs,
  caList,
  dnsList,
  onChange,
  onConfig,
  onMove,
  onDelete,
}: {
  step: WorkflowStep;
  index: number;
  total: number;
  graphMode?: boolean;
  certs: Array<{ id: string; name: string; primary_domain: string }>;
  caList: Array<{ id: string; name: string; status: string }>;
  dnsList: Array<{ id: string; name: string; provider: string }>;
  onChange: (patch: Partial<WorkflowStep>) => void;
  onConfig: (key: string, value: unknown) => void;
  onMove: (dir: -1 | 1) => void;
  onDelete: () => void;
}) {
  const { t } = useTranslation();
  const cfg = (step.config ?? {}) as Record<string, unknown>;
  const certID = String(cfg.certificate_id ?? "");
  return (
    <Card>
      <CardContent className="space-y-3 p-4">
        <div className="flex flex-wrap items-end gap-2">
          <Badge variant="outline" className="h-8 items-center gap-1"><GitBranch className="h-3 w-3" /> {graphMode ? t("workflow.node") : index + 1}</Badge>
          <Field label={t("workflow.stepType")} className="w-32">
            <Select value={step.type} onChange={(e) => onChange({ type: e.target.value as WorkflowStepType, config: defaultConfig(e.target.value as WorkflowStepType) })}>
              {stepTypes.map((type) => <option key={type} value={type}>{t(`workflow.types.${type}`)}</option>)}
            </Select>
          </Field>
          <Field label={t("common.name")} className="min-w-40 flex-1">
            <Input value={step.name ?? ""} onChange={(e) => onChange({ name: e.target.value })} />
          </Field>
          {!graphMode && (
            <>
              <Field label={t("workflow.when")} className="w-36">
                <Select value={step.when ?? "success"} onChange={(e) => onChange({ when: e.target.value as WorkflowStep["when"] })}>
                  <option value="success">{t("workflow.whenSuccess")}</option>
                  <option value="failure">{t("workflow.whenFailure")}</option>
                  <option value="always">{t("workflow.whenAlways")}</option>
                </Select>
              </Field>
              <Field label={t("workflow.parallelGroup")} className="w-36">
                <Input value={step.parallel_group ?? ""} onChange={(e) => onChange({ parallel_group: e.target.value })} placeholder="default" />
              </Field>
            </>
          )}
          {!graphMode && (
            <div className="mb-1 flex items-center gap-2">
              <Switch checked={Boolean(step.continue_on_error)} onCheckedChange={(v) => onChange({ continue_on_error: v })} />
              <span className="text-xs text-muted-foreground">{t("workflow.continueOnError")}</span>
            </div>
          )}
          <div className="mb-0.5 flex gap-1">
            {!graphMode && (
              <>
                <Button variant="ghost" size="icon" disabled={index === 0} onClick={() => onMove(-1)}><ArrowUp className="h-4 w-4" /></Button>
                <Button variant="ghost" size="icon" disabled={index === total - 1} onClick={() => onMove(1)}><ArrowDown className="h-4 w-4" /></Button>
              </>
            )}
            <Button variant="ghost" size="icon" onClick={onDelete}><Trash2 className="h-4 w-4 text-destructive" /></Button>
          </div>
        </div>

        {step.type === "apply" && (
          <div className="grid gap-3 sm:grid-cols-3">
            <Field label={t("workflow.certificate")} hint={t("workflow.inlineHint")}>
              <Select value={certID} onChange={(e) => onConfig("certificate_id", e.target.value)}>
                <option value="">{t("workflow.inlineCertificate")}</option>
                {certs.map((c) => <option key={c.id} value={c.id}>{c.name} ({c.primary_domain})</option>)}
              </Select>
            </Field>
            <Field label={t("workflow.mode")}>
              <Select value={String(cfg.mode ?? "auto")} onChange={(e) => onConfig("mode", e.target.value)}>
                <option value="auto">auto</option>
                <option value="issue">issue</option>
                <option value="renew">renew</option>
              </Select>
            </Field>
            <div className="flex items-end gap-2 pb-1">
              <Switch checked={Boolean(cfg.force)} onCheckedChange={(v) => onConfig("force", v)} />
              <span className="text-sm">{t("workflow.force")}</span>
            </div>
            {!certID && (
              <>
                <Field label={t("cert.domain")}>
                  <Input value={String(cfg.primary_domain ?? "")} onChange={(e) => onConfig("primary_domain", e.target.value)} />
                </Field>
                <Field label={t("workflow.keyType")}>
                  <Select value={String(cfg.key_type ?? "ec-256")} onChange={(e) => onConfig("key_type", e.target.value)}>
                    {["ec-256", "ec-384", "rsa-2048", "rsa-3072", "rsa-4096"].map((k) => <option key={k} value={k}>{k}</option>)}
                  </Select>
                </Field>
                <Field label={t("workflow.ca")}>
                  <Select value={String(cfg.ca_account_id ?? "")} onChange={(e) => onConfig("ca_account_id", e.target.value)}>
                    <option value="">—</option>
                    {caList.filter((c) => c.status !== "disabled").map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}
                  </Select>
                </Field>
                <Field label={t("workflow.challenge")}>
                  <Select value={String(cfg.challenge_type ?? "dns-01")} onChange={(e) => onConfig("challenge_type", e.target.value)}>
                    <option value="dns-01">dns-01</option>
                    <option value="http-01-webroot">http-01-webroot</option>
                    <option value="http-01-standalone">http-01-standalone</option>
                    <option value="tls-alpn-01">tls-alpn-01</option>
                  </Select>
                </Field>
                {String(cfg.challenge_type ?? "dns-01") === "dns-01" ? (
                  <Field label={t("workflow.dnsCredential")}>
                    <Select value={String(cfg.dns_credential_id ?? "")} onChange={(e) => onConfig("dns_credential_id", e.target.value)}>
                      <option value="">—</option>
                      {dnsList.map((d) => <option key={d.id} value={d.id}>{d.name} ({d.provider})</option>)}
                    </Select>
                  </Field>
                ) : (
                  <Field label={t("cert.webroot")}>
                    <Input value={String(cfg.webroot ?? "")} onChange={(e) => onConfig("webroot", e.target.value)} />
                  </Field>
                )}
              </>
            )}
          </div>
        )}

        {step.type === "deploy" && (
          <div className="grid gap-3 sm:grid-cols-2">
            <Field label={t("workflow.certificate")} hint={t("workflow.inheritHint")}>
              <Select value={certID} onChange={(e) => onConfig("certificate_id", e.target.value)}>
                <option value="">{t("workflow.inherit")}</option>
                {certs.map((c) => <option key={c.id} value={c.id}>{c.name} ({c.primary_domain})</option>)}
              </Select>
            </Field>
            <Field label={t("workflow.revision")} hint={t("workflow.revisionHint")}>
              <Input value={String(cfg.revision_id ?? "")} onChange={(e) => onConfig("revision_id", e.target.value)} />
            </Field>
          </div>
        )}

        {step.type === "notify" && (
          <div className="grid gap-3">
            <Field label={t("workflow.event")}>
              <Input value={String(cfg.event ?? "workflow.notify")} onChange={(e) => onConfig("event", e.target.value)} />
            </Field>
            <Field label={t("workflow.titleField")}>
              <Input value={String(cfg.title ?? "")} onChange={(e) => onConfig("title", e.target.value)} />
            </Field>
            <Field label={t("workflow.body")}>
              <Textarea rows={3} value={String(cfg.body ?? "")} onChange={(e) => onConfig("body", e.target.value)} />
            </Field>
          </div>
        )}

        {step.type === "verify" && (
          <div className="grid gap-3 sm:grid-cols-2">
            <Field label={t("workflow.certificate")} hint={t("workflow.inheritHint")}>
              <Select value={certID} onChange={(e) => onConfig("certificate_id", e.target.value)}>
                <option value="">{t("workflow.inherit")}</option>
                {certs.map((c) => <option key={c.id} value={c.id}>{c.name} ({c.primary_domain})</option>)}
              </Select>
            </Field>
            <Field label={t("workflow.minDays")}>
              <Input type="number" min={0} max={365} value={Number(cfg.min_days ?? 7)} onChange={(e) => onConfig("min_days", Number(e.target.value))} />
            </Field>
          </div>
        )}

        {step.type === "wait" && (
          <Field label={t("workflow.seconds")}>
            <Input type="number" min={1} max={86400} value={Number(cfg.seconds ?? 5)} onChange={(e) => onConfig("seconds", Number(e.target.value))} />
          </Field>
        )}
      </CardContent>
    </Card>
  );
}

function emptyForm(): EditForm {
  const apply = newNode("apply", { x: 80, y: 120 });
  const deploy = newNode("deploy", { x: 380, y: 120 });
  const notify = newNode("notify", { x: 680, y: 120 });
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
  };
}

function RunsDialog({ workflow, canRun, onOpenChange }: { workflow: Workflow | null; canRun: boolean; onOpenChange: (open: boolean) => void }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const { data: runs } = useWorkflowRuns(workflow?.id);
  const [runID, setRunID] = React.useState("");
  const { data: detail } = useWorkflowRun(runID || undefined);

  React.useEffect(() => {
    setRunID("");
  }, [workflow?.id]);

  const cancel = async (run: WorkflowRun) => {
    try {
      await request(`/workflow-runs/${run.id}/cancel`, { method: "POST", body: {} });
      toast.success(t("workflow.canceled"));
      void qc.invalidateQueries({ queryKey: qk.workflowRuns(workflow?.id ?? "") });
    } catch (err) {
      toast.error(errorMessage(err));
    }
  };

  return (
    <Dialog open={Boolean(workflow)} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[90vh] max-w-4xl overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{workflow?.name} · {t("workflow.runs")}</DialogTitle>
          <DialogDescription>{t("workflow.runsHint")}</DialogDescription>
        </DialogHeader>
        <Table>
          <thead>
            <tr>
              <Th>{t("common.status")}</Th>
              <Th>{t("workflow.trigger")}</Th>
              <Th>{t("common.createdAt")}</Th>
              <Th>{t("job.duration")}</Th>
              <Th />
            </tr>
          </thead>
          <tbody>
            {(runs?.items ?? []).map((r) => (
              <tr key={r.id}>
                <Td><Badge variant={statusVariant(r.status)}>{t(`status.${r.status}`)}</Badge></Td>
                <Td className="text-muted-foreground">{r.trigger_type}</Td>
                <Td className="text-muted-foreground">{formatDateTime(r.created_at)}</Td>
                <Td className="text-muted-foreground">{duration(r.started_at, r.finished_at)}</Td>
                <Td className="text-right">
                  <div className="flex justify-end gap-1">
                    <Button variant="ghost" size="sm" onClick={() => setRunID(r.id)}>{t("common.details")}</Button>
                    {canRun && ["queued", "running"].includes(r.status) && (
                      <Button variant="ghost" size="sm" onClick={() => void cancel(r)}>{t("common.cancel")}</Button>
                    )}
                  </div>
                </Td>
              </tr>
            ))}
          </tbody>
        </Table>
        {detail && (
          <div className="mt-4 space-y-2">
            <div className="flex items-center justify-between">
              <h3 className="text-sm font-semibold">{t("workflow.runDetail")}</h3>
              <a className="text-xs text-primary underline" href={detail.log_url} target="_blank" rel="noreferrer">{t("workflow.log")}</a>
            </div>
            {detail.steps.map((s) => (
              <div key={s.id} className="rounded-md border border-border p-3 text-sm">
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <div className="font-medium">{s.name || s.step_id} <span className="text-xs text-muted-foreground">({s.type})</span></div>
                  <Badge variant={statusVariant(s.status)}>{t(`status.${s.status}`)}</Badge>
                </div>
                {s.error && <p className="mt-1 text-xs text-destructive">{s.error}</p>}
                <p className="mt-1 text-xs text-muted-foreground">
                  {t("workflow.attempts")}: {s.attempts} · {t("workflow.jobs")}: {s.job_ids.length}
                </p>
              </div>
            ))}
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}

function duration(start: number | null, end: number | null): string {
  if (!start) return "—";
  const s = Math.max(0, (end ?? Math.floor(Date.now() / 1000)) - start);
  if (s < 60) return `${s}s`;
  return `${Math.floor(s / 60)}m ${s % 60}s`;
}
