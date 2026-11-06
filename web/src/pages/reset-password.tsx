import * as React from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { LockKeyhole } from "lucide-react";
import { ApiError, errorMessage, request } from "@/api/client";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Field, Input } from "@/components/ui/input";
import { ErrorBox } from "@/components/ui/misc";

export function ResetPasswordPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const token = params.get("token") ?? "";
  const [password, setPassword] = React.useState("");
  const [confirm, setConfirm] = React.useState("");
  const [error, setError] = React.useState("");
  const [busy, setBusy] = React.useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError("");
    if (password !== confirm) {
      setError(t("auth.confirmPassword"));
      return;
    }
    setBusy(true);
    try {
      await request("/auth/reset-password", { method: "POST", body: { token, password } });
      navigate("/login", { replace: true });
    } catch (err) {
      if (err instanceof ApiError && err.code === "E_INVALID_TOKEN") {
        setError(t("auth.invalidToken"));
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
            <LockKeyhole className="h-7 w-7 text-primary" />
          </div>
          <h1 className="text-2xl font-semibold">{t("auth.resetTitle")}</h1>
          <p className="max-w-sm text-sm text-muted-foreground">{t("auth.resetHint")}</p>
        </div>
        <Card>
          <CardHeader>
            <CardTitle>{t("auth.newPassword")}</CardTitle>
            <CardDescription>{t("auth.passwordHint")}</CardDescription>
          </CardHeader>
          <CardContent>
            {!token ? (
              <div className="space-y-4">
                <ErrorBox message={t("auth.invalidToken")} />
                <Button asChild variant="outline" className="w-full">
                  <Link to="/forgot-password">{t("auth.forgotPassword")}</Link>
                </Button>
              </div>
            ) : (
              <form className="space-y-4" onSubmit={submit}>
                {error && <ErrorBox message={error} />}
                <Field label={t("auth.newPassword")} htmlFor="password" hint={t("auth.passwordHint")}>
                  <Input
                    id="password"
                    type="password"
                    autoComplete="new-password"
                    minLength={12}
                    value={password}
                    onChange={(e) => setPassword(e.target.value)}
                    required
                    autoFocus
                  />
                </Field>
                <Field label={t("auth.confirmPassword")} htmlFor="confirm">
                  <Input
                    id="confirm"
                    type="password"
                    autoComplete="new-password"
                    minLength={12}
                    value={confirm}
                    onChange={(e) => setConfirm(e.target.value)}
                    required
                  />
                </Field>
                <Button type="submit" className="w-full" disabled={busy}>
                  {busy ? t("common.loading") : t("auth.resetPassword")}
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
