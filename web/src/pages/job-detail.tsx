import * as React from "react";
import { Link, useParams } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useJob } from "@/api/queries";
import { Badge, statusVariant } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { BackLink, ErrorBox, PageHeader, Spinner } from "@/components/ui/misc";
import { formatDateTime } from "@/lib/format";
import { cn } from "@/lib/utils";

interface LogLine {
  seq: number;
  stream: string;
  line: string;
}

export function JobDetailPage() {
  const { id = "" } = useParams();
  const { t } = useTranslation();
  const { data: job, isLoading } = useJob(id);
  const [lines, setLines] = React.useState<LogLine[]>([]);
  const [liveStatus, setLiveStatus] = React.useState<string>("");
  const [done, setDone] = React.useState(false);
  const [connected, setConnected] = React.useState(false);
  const terminalRef = React.useRef<HTMLDivElement>(null);

  React.useEffect(() => {
    if (!id) return;
    setLines([]);
    setDone(false);
    const es = new EventSource(`/api/v1/jobs/${id}/events`);
    es.onopen = () => setConnected(true);
    es.onerror = () => setConnected(false);
    es.addEventListener("job.log", (ev) => {
      try {
        const data = JSON.parse((ev as MessageEvent).data) as LogLine;
        setLines((prev) => (prev.some((l) => l.seq === data.seq) ? prev : [...prev, data]));
      } catch {
        /* ignore malformed */
      }
    });
    es.addEventListener("job.status", (ev) => {
      try {
        const data = JSON.parse((ev as MessageEvent).data) as { status: string };
        setLiveStatus(data.status);
      } catch {
        /* ignore */
      }
    });
    es.addEventListener("job.done", (ev) => {
      try {
        const data = JSON.parse((ev as MessageEvent).data) as { status: string };
        setLiveStatus(data.status);
      } catch {
        /* ignore */
      }
      setDone(true);
      es.close();
    });
    return () => es.close();
  }, [id]);

  React.useEffect(() => {
    const el = terminalRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [lines]);

  if (isLoading) {
    return (
      <div className="flex justify-center py-20">
        <Spinner className="h-6 w-6" />
      </div>
    );
  }
  if (!job) return <ErrorBox message={t("errors.notFound")} />;

  const status = liveStatus || job.status;
  const isLive = !done && (status === "running" || status === "queued");

  return (
    <div>
      <div className="mb-2">
        <BackLink to="/jobs">{t("job.title")}</BackLink>
      </div>
      <PageHeader
        title={t(`job.types.${job.type}`)}
        subtitle={job.id}
        actions={
          <div className="flex items-center gap-2">
            <Badge variant={statusVariant(status)}>{t(`status.${status}`)}</Badge>
            {isLive && (
              <Badge variant="warning" className="gap-1">
                <span className={cn("h-1.5 w-1.5 rounded-full bg-amber-500", connected && "animate-pulse")} />
                {t("job.live")}
              </Badge>
            )}
          </div>
        }
      />

      {job.error_message && (
        <div className="mb-4">
          <ErrorBox message={`${job.error_code ?? "E_UNKNOWN"}: ${job.error_message}`} />
        </div>
      )}

      <div className="grid gap-6 lg:grid-cols-3">
        <Card className="lg:col-span-2">
          <CardHeader className="flex-row items-center justify-between">
            <CardTitle>{t("job.log")}</CardTitle>
            <ButtonRefreshLines lines={lines} />
          </CardHeader>
          <CardContent>
            <div
              ref={terminalRef}
              className="h-[55vh] overflow-y-auto rounded-md border border-border bg-[#0b0f14] p-3 font-mono text-xs leading-relaxed text-slate-200 scrollbar-thin"
            >
              {lines.length === 0 ? (
                <p className="text-slate-500">{t("job.noLogs")}</p>
              ) : (
                lines.map((line) => (
                  <div key={line.seq} className="whitespace-pre-wrap break-all">
                    <span className="mr-2 select-none text-slate-600">{String(line.seq).padStart(3, "0")}</span>
                    <span className={line.line.includes("failed") ? "text-red-400" : line.line.includes("succeeded") ? "text-emerald-400" : undefined}>
                      {line.line}
                    </span>
                  </div>
                ))
              )}
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>{t("common.details")}</CardTitle>
          </CardHeader>
          <CardContent className="space-y-3 text-sm">
            <Row label={t("job.id")} value={<span className="break-all font-mono text-[11px]">{job.id}</span>} />
            <Row label={t("job.type")} value={t(`job.types.${job.type}`)} />
            <Row label={t("common.status")} value={<Badge variant={statusVariant(status)}>{t(`status.${status}`)}</Badge>} />
            <Row label={t("job.attempts")} value={`${job.attempts} / ${job.max_attempts}`} />
            <Row label={t("job.errorCode")} value={job.error_code ?? "—"} />
            <Row label={t("common.createdAt")} value={formatDateTime(job.created_at)} />
            <Row label={t("job.startedAt")} value={formatDateTime(job.started_at)} />
            <Row label={t("job.finishedAt")} value={formatDateTime(job.finished_at)} />
            {job.certificate_id && (
              <Row
                label={t("cert.title")}
                value={
                  <Link to={`/certificates/${job.certificate_id}`} className="text-primary hover:underline">
                    {job.certificate_id}
                  </Link>
                }
              />
            )}
          </CardContent>
        </Card>
      </div>
    </div>
  );
}

function Row({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div>
      <p className="text-muted-foreground">{label}</p>
      <div className="mt-0.5">{value}</div>
    </div>
  );
}

function ButtonRefreshLines({ lines }: { lines: LogLine[] }) {
  return <span className="text-xs text-muted-foreground">{lines.length} lines</span>;
}
