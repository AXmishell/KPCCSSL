import * as React from "react";
import { useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { ShieldCheck } from "lucide-react";
import { useAuth } from "@/auth";
import { errorMessage } from "@/api/client";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Field, Input } from "@/components/ui/input";
import { ErrorBox } from "@/components/ui/misc";

export function SetupPage() {
  const { t } = useTranslation();
  const { setup } = useAuth();
  const navigate = useNavigate();
  const [token, setToken] = React.useState("");
  const [email, setEmail] = React.useState("");
  const [password, setPassword] = React.useState("");
  const [confirm, setConfirm] = React.useState("");
  const [error, setError] = React.useState("");
  const [busy, setBusy] = React.useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (password !== confirm) {
      setError(t("auth.confirmPassword"));
      return;
    }
    setBusy(true);
    setError("");
    try {
      await setup(token, email, password);
      navigate("/", { replace: true });
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="flex min-h-full items-center justify-center p-4">
      <div className="w-full max-w-md">
        <div className="mb-6 flex flex-col items-center gap-2 text-center">
          <div className="flex h-12 w-12 items-center justify-center rounded-xl bg-primary/15">
            <ShieldCheck className="h-7 w-7 text-primary" />
          </div>
          <h1 className="text-2xl font-semibold">{t("auth.setupTitle")}</h1>
          <p className="max-w-sm text-sm text-muted-foreground">{t("auth.setupHint")}</p>
        </div>
        <Card>
          <CardHeader>
            <CardTitle>{t("auth.setup")}</CardTitle>
            <CardDescription>{t("profile.role")}: admin</CardDescription>
          </CardHeader>
          <CardContent>
            <form className="space-y-4" onSubmit={submit}>
              {error && <ErrorBox message={error} />}
              <Field label={t("auth.setupToken")} htmlFor="token">
                <Input id="token" value={token} onChange={(e) => setToken(e.target.value)} required autoFocus />
              </Field>
              <Field label={t("auth.email")} htmlFor="email">
                <Input id="email" type="email" value={email} onChange={(e) => setEmail(e.target.value)} required />
              </Field>
              <Field label={t("auth.password")} htmlFor="password" hint={t("auth.passwordHint")}>
                <Input
                  id="password"
                  type="password"
                  autoComplete="new-password"
                  minLength={12}
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  required
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
                {busy ? t("common.loading") : t("auth.setup")}
              </Button>
            </form>
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
