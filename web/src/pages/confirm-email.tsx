import * as React from "react";
import { Link, useSearchParams } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { MailCheck } from "lucide-react";
import { useAuth } from "@/auth";
import { ApiError, errorMessage, request } from "@/api/client";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { ErrorBox, Spinner } from "@/components/ui/misc";

export function ConfirmEmailPage() {
  const { t } = useTranslation();
  const { refresh } = useAuth();
  const [params] = useSearchParams();
  const token = params.get("token") ?? "";
  const [busy, setBusy] = React.useState(Boolean(token));
  const [email, setEmail] = React.useState("");
  const [error, setError] = React.useState("");
  const started = React.useRef(false);

  React.useEffect(() => {
    if (!token || started.current) return;
    started.current = true;
    request<{ email: string }>("/auth/confirm-email", { method: "POST", body: { token } })
      .then((res) => {
        setEmail(res.email);
        void refresh();
      })
      .catch((err) => {
        if (err instanceof ApiError && err.code === "E_INVALID_TOKEN") {
          setError(t("auth.invalidToken"));
        } else if (err instanceof ApiError && err.code === "E_CONFLICT") {
          setError(t("auth.emailTaken"));
        } else {
          setError(errorMessage(err));
        }
      })
      .finally(() => setBusy(false));
  }, [token, refresh, t]);

  return (
    <div className="flex min-h-full items-center justify-center p-4">
      <div className="w-full max-w-md">
        <div className="mb-6 flex flex-col items-center gap-2 text-center">
          <div className="flex h-12 w-12 items-center justify-center rounded-xl bg-primary/15">
            <MailCheck className="h-7 w-7 text-primary" />
          </div>
          <h1 className="text-2xl font-semibold">{t("auth.confirmEmailTitle")}</h1>
        </div>
        <Card>
          <CardHeader>
            <CardTitle>{t("auth.confirmEmailTitle")}</CardTitle>
            <CardDescription>{t("auth.confirmEmailHint")}</CardDescription>
          </CardHeader>
          <CardContent>
            {!token ? (
              <ErrorBox message={t("auth.invalidToken")} />
            ) : busy ? (
              <div className="flex justify-center py-6">
                <Spinner className="h-6 w-6" />
              </div>
            ) : error ? (
              <div className="space-y-4">
                <ErrorBox message={error} />
                <Button asChild variant="outline" className="w-full">
                  <Link to="/login">{t("auth.backToLogin")}</Link>
                </Button>
              </div>
            ) : (
              <div className="space-y-4">
                <p className="text-sm">
                  {t("auth.emailChanged")} <span className="font-medium">{email}</span>
                </p>
                <p className="text-sm text-muted-foreground">{t("auth.emailChangedHint")}</p>
                <Button asChild className="w-full">
                  <Link to="/login">{t("auth.backToLogin")}</Link>
                </Button>
              </div>
            )}
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
