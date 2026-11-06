import * as React from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { errorMessage, request } from "@/api/client";
import { qk, useWorkflowRun, useWorkflowRuns } from "@/api/queries";
import type { Workflow, WorkflowRun } from "@/api/types";
import { Badge, statusVariant } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { ErrorBox, Table, Td, Th } from "@/components/ui/misc";
import { formatDateTime } from "@/lib/format";
import { duration } from "./workflow-utils";

export function RunsDialog({ workflow, canRun, onOpenChange }: { workflow: Workflow | null; canRun: boolean; onOpenChange: (open: boolean) => void }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const { data: runs, error: runsError } = useWorkflowRuns(workflow?.id);
  const [runID, setRunID] = React.useState("");
  const { data: detail, error: detailError } = useWorkflowRun(runID || undefined);

  React.useEffect(() => {
    setRunID("");
  }, [workflow?.id]);

  // Show the newest run without an extra click once the list arrives.
  React.useEffect(() => {
    if (!runID && runs?.items.length) setRunID(runs.items[0].id);
  }, [runID, runs?.items]);

  const cancel = async (run: WorkflowRun) => {
    try {
      await request(`/workflow-runs/${run.id}/cancel`, { method: "POST", body: {} });
      toast.success(t("workflow.canceled"));
      void qc.invalidateQueries({ queryKey: qk.workflowRuns(workflow?.id ?? "") });
      void qc.invalidateQueries({ queryKey: qk.workflowRun(run.id) });
      void qc.invalidateQueries({ queryKey: qk.workflows });
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
        {runsError && <ErrorBox message={errorMessage(runsError)} />}
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
                <Td className="text-muted-foreground">{t(`workflow.${r.trigger_type}`)}</Td>
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
        {detailError && <ErrorBox message={errorMessage(detailError)} />}
        {detail && (
          <div className="mt-4 space-y-2">
            <div className="flex items-center justify-between">
              <h3 className="text-sm font-semibold">{t("workflow.runDetail")}</h3>
              <a className="text-xs text-primary underline" href={detail.log_url} target="_blank" rel="noreferrer">{t("workflow.log")}</a>
            </div>
            {detail.run.error && <ErrorBox message={detail.run.error} />}
            {detail.steps.map((s) => {
              const retryIn = s.status === "pending" && s.available_at ? s.available_at - Math.floor(Date.now() / 1000) : 0;
              return (
                <div key={s.id} className="rounded-md border border-border p-3 text-sm">
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <div className="font-medium">{s.name || s.step_id} <span className="text-xs text-muted-foreground">({s.type})</span></div>
                    <Badge variant={statusVariant(s.status)}>{t(`status.${s.status}`)}</Badge>
                  </div>
                  {s.error && <p className="mt-1 text-xs text-destructive">{s.error}</p>}
                  <p className="mt-1 text-xs text-muted-foreground">
                    {t("workflow.attempts")}: {s.attempts} · {t("workflow.jobs")}: {s.job_ids.length} · {duration(s.started_at, s.finished_at)}
                    {retryIn > 0 ? ` · ${t("workflow.retryIn", { seconds: retryIn })}` : ""}
                  </p>
                  {s.job_ids.length > 0 && (
                    <div className="mt-1 flex flex-wrap gap-2">
                      {s.job_ids.map((jobID) => (
                        <Link key={jobID} className="text-xs text-primary underline" to={`/jobs/${jobID}`}>
                          {t("workflow.job")} {jobID.slice(0, 8)}
                        </Link>
                      ))}
                    </div>
                  )}
                  {s.output && Object.keys(s.output).length > 0 && (
                    <details className="mt-2">
                      <summary className="cursor-pointer text-xs text-muted-foreground">{t("workflow.output")}</summary>
                      <pre className="mt-1 overflow-x-auto rounded bg-muted/40 p-2 text-[11px]">{JSON.stringify(s.output, null, 2)}</pre>
                    </details>
                  )}
                </div>
              );
            })}
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}
