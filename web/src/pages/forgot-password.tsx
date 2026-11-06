import * as React from "react";
import { Link } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { KeyRound } from "lucide-react";
import { ApiError, errorMessage, request } from "@/api/client";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Field, Input } from "@/components/ui/input";
import { ErrorBox } from "@/components/ui/misc";

export function ForgotPasswordPage() {
  const { t } = useTranslation();
  const [email, setEmail] = React.useState("");
  const [sent, setSent] = React.useState(false);
  const [error, setError] = React.useState("");
  const [busy, setBusy] = React.useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await request("/auth/forgot-password", { method: "POST", body: { email } });
      setSent(true);
    } catch (err) {
      if (err instanceof ApiError && err.code === "E_MAIL_NOT_CONFIGURED") {
        setError(t("auth.mailNotConfigured"));
      } else {
        setError(errorMessage(err));
      }
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="flex min-h-full items-center justify-center p-4">
      <div className="w-full max-w-md">
        <div className="mb-6 flex flex-col items-center gap-2 text-center">
          <div className="flex h-12 w-12 items-center justify-center rounded-xl bg-primary/15">
            <KeyRound className="h-7 w-7 text-primary" />
          </div>
          <h1 className="text-2xl font-semibold">{t("auth.forgotTitle")}</h1>
          <p className="max-w-sm text-sm text-muted-foreground">{t("auth.forgotHint")}</p>
        </div>
        <Card>
          <CardHeader>
            <CardTitle>{t("auth.forgotPassword")}</CardTitle>
            <CardDescription>{sent ? t("auth.resetSent") : t("auth.forgotHint")}</CardDescription>
          </CardHeader>
          <CardContent>
            {sent ? (
              <div className="space-y-4">
                <p className="text-sm text-muted-foreground">{t("auth.resetSentHint")}</p>
                <Button asChild className="w-full" variant="outline">
                  <Link to="/login">{t("auth.backToLogin")}</Link>
                </Button>
              </div>
            ) : (
              <form className="space-y-4" onSubmit={submit}>
                {error && <ErrorBox message={error} />}
                <Field label={t("auth.email")} htmlFor="email">
                  <Input
                    id="email"
                    type="email"
                    autoComplete="email"
                    value={email}
                    onChange={(e) => setEmail(e.target.value)}
                    required
                    autoFocus
                  />
                </Field>
                <Button type="submit" className="w-full" disabled={busy}>
                  {busy ? t("common.loading") : t("auth.sendResetLink")}
                </Button>
                <div className="text-center">
                  <Link to="/login" className="text-xs text-muted-foreground underline-offset-2 hover:underline">
                    {t("auth.backToLogin")}
                  </Link>
                </div>
              </form>
            )}
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
