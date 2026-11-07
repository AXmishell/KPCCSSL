import * as React from "react";
import { useTranslation } from "react-i18next";
import { useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { History, Pencil, Play, Plus, Trash2 } from "lucide-react";
import { useAuth } from "@/auth";
import { errorMessage, request } from "@/api/client";
import { qk, useWorkflows } from "@/api/queries";
import type { Workflow } from "@/api/types";
import { Badge, statusVariant } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { EmptyState, ErrorBox, PageHeader, Spinner, Table, Td, Th } from "@/components/ui/misc";
import { fromNow } from "@/lib/format";
import { CertSectionTabs } from "@/components/cert-tabs";
import { WorkflowEditor } from "./workflow-editor";
import { RunsDialog } from "./workflow-runs";

export function WorkflowsPage() {
  const { t, i18n } = useTranslation();
  const { user } = useAuth();
  const qc = useQueryClient();
  const { data, isLoading, error } = useWorkflows();
  const [editOpen, setEditOpen] = React.useState(false);
  const [editing, setEditing] = React.useState<Workflow | null>(null);
  const [runsFor, setRunsFor] = React.useState<Workflow | null>(null);
  const [starting, setStarting] = React.useState<string[]>([]);
  const isAdmin = user?.role === "admin";
  const canRun = user?.role === "admin" || user?.role === "operator";

  const invalidate = () => void qc.invalidateQueries({ queryKey: qk.workflows });
  const isRunActive = (w: Workflow) =>
    starting.includes(w.id) || w.last_run?.status === "queued" || w.last_run?.status === "running";

  const run = async (w: Workflow) => {
    if (starting.includes(w.id)) return;
    setStarting((s) => [...s, w.id]);
    try {
      await request(`/workflows/${w.id}/run`, { method: "POST", body: {} });
      toast.success(t("workflow.queued"));
      invalidate();
    } catch (err) {
      toast.error(errorMessage(err));
    } finally {
      setStarting((s) => s.filter((id) => id !== w.id));
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
      <CertSectionTabs />
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
                <Td className="tabular-nums">{w.definition?.nodes?.length ?? w.definition?.steps?.length ?? 0}</Td>
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
                      <Button
                        variant="outline"
                        size="sm"
                        disabled={isRunActive(w)}
                        title={isRunActive(w) ? t("workflow.alreadyRunning") : undefined}
                        onClick={() => void run(w)}
                      >
                        <Play className="h-3.5 w-3.5" /> {t("workflow.run")}
                      </Button>
                    )}
                    <Button variant="ghost" size="sm" onClick={() => setRunsFor(w)}>
                      <History className="h-3.5 w-3.5" /> {t("workflow.runs")}
                    </Button>
                    {isAdmin && (
                      <>
                        <Button variant="ghost" size="sm" aria-label={t("common.edit")} title={t("common.edit")} onClick={() => { setEditing(w); setEditOpen(true); }}>
                          <Pencil className="h-3.5 w-3.5" />
                        </Button>
                        <Button variant="ghost" size="icon" aria-label={t("common.delete")} title={t("common.delete")} onClick={() => void remove(w)}>
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

      {editOpen && (
        <WorkflowEditor open={editOpen} onOpenChange={setEditOpen} workflow={editing} onSaved={invalidate} />
      )}
      <RunsDialog key={runsFor?.id ?? "none"} workflow={runsFor} canRun={canRun} onOpenChange={(open) => !open && setRunsFor(null)} />
    </div>
  );
}
