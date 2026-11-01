import { Link } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { ArrowRight, FileCheck2, FileWarning, Plus, ShieldAlert, TimerReset } from "lucide-react";
import { useAuth } from "@/auth";
import { useDashboard } from "@/api/queries";
import { Badge, statusVariant } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { EmptyState, PageHeader, Spinner, Table, Td, Th } from "@/components/ui/misc";
import { formatDateTime, fromNow } from "@/lib/format";

export function DashboardPage() {
  const { t, i18n } = useTranslation();
  const { user } = useAuth();
  const { data, isLoading } = useDashboard();

  const cards = [
    { key: "total", icon: FileCheck2, value: data?.certificates.total ?? 0, tone: "text-primary" },
    { key: "active", icon: ShieldAlert, value: data?.certificates.active ?? 0, tone: "text-emerald-500" },
    { key: "expiring", icon: TimerReset, value: data?.certificates.expiring_30d ?? 0, tone: "text-amber-500" },
    { key: "failed", icon: FileWarning, value: data?.certificates.failed ?? 0, tone: "text-destructive" },
  ];

  return (
    <div>
      <PageHeader
        title={t("dashboard.title")}
        subtitle={t("dashboard.subtitle")}
        actions={
          <Button asChild>
            <Link to="/certificates/new">
              <Plus className="h-4 w-4" /> {t("dashboard.newCertificate")}
            </Link>
          </Button>
        }
      />
      <p className="mb-6 text-sm text-muted-foreground">{t("dashboard.welcome", { email: user?.email })}</p>

      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        {cards.map((card) => (
          <Card key={card.key}>
            <CardContent className="flex items-center justify-between p-5">
              <div>
                <p className="text-sm text-muted-foreground">{t(`dashboard.${card.key}`)}</p>
                <p className="mt-1 text-3xl font-semibold tabular-nums">{card.value}</p>
              </div>
              <card.icon className={`h-8 w-8 ${card.tone}`} />
            </CardContent>
          </Card>
        ))}
      </div>

      <div className="mt-8 grid gap-6 lg:grid-cols-3">
        <Card className="lg:col-span-2">
          <CardHeader className="flex-row items-center justify-between">
            <CardTitle>{t("dashboard.recentJobs")}</CardTitle>
            <Button asChild variant="ghost" size="sm">
              <Link to="/jobs">
                {t("common.view")} <ArrowRight className="h-3.5 w-3.5" />
              </Link>
            </Button>
          </CardHeader>
          <CardContent>
            {isLoading ? (
              <div className="flex justify-center py-8">
                <Spinner className="h-5 w-5" />
              </div>
            ) : !data?.recent_jobs?.length ? (
              <EmptyState description={t("job.empty")} />
            ) : (
              <Table>
                <thead>
                  <tr>
                    <Th>{t("job.type")}</Th>
                    <Th>{t("common.status")}</Th>
                    <Th>{t("common.createdAt")}</Th>
                    <Th />
                  </tr>
                </thead>
                <tbody>
                  {data.recent_jobs.map((job) => (
                    <tr key={job.id}>
                      <Td>{t(`job.types.${job.type}`)}</Td>
                      <Td>
                        <Badge variant={statusVariant(job.status)}>{t(`status.${job.status}`)}</Badge>
                      </Td>
                      <Td className="text-muted-foreground" title={formatDateTime(job.created_at)}>
                        {fromNow(job.created_at, i18n.language)}
                      </Td>
                      <Td className="text-right">
                        <Button asChild variant="ghost" size="sm">
                          <Link to={`/jobs/${job.id}`}>{t("common.details")}</Link>
                        </Button>
                      </Td>
                    </tr>
                  ))}
                </tbody>
              </Table>
            )}
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>{t("dashboard.quickActions")}</CardTitle>
          </CardHeader>
          <CardContent className="flex flex-col gap-2">
            <Button asChild variant="outline" className="justify-start">
              <Link to="/certificates/new">
                <Plus className="h-4 w-4" /> {t("dashboard.newCertificate")}
              </Link>
            </Button>
            <Button asChild variant="outline" className="justify-start">
              <Link to="/ca-accounts">
                <ShieldAlert className="h-4 w-4" /> {t("dashboard.addCA")}
              </Link>
            </Button>
            <Button asChild variant="outline" className="justify-start">
              <Link to="/dns-credentials">
                <FileCheck2 className="h-4 w-4" /> {t("dashboard.addDNS")}
              </Link>
            </Button>
            <Button asChild variant="ghost" className="justify-start">
              <Link to="/jobs">
                <ArrowRight className="h-4 w-4" /> {t("dashboard.viewJobs")}
              </Link>
            </Button>
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
