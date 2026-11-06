import * as React from "react";
import { useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { toast } from "sonner";
import { ArrowLeft, ArrowRight, Check, Loader2 } from "lucide-react";
import { useCA, useDNS, useDNSProviders, useInfo } from "@/api/queries";
import { errorMessage, request } from "@/api/client";
import type { CertInput, Certificate, ChallengeType } from "@/api/types";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Field, Input, Select, Textarea } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { Badge } from "@/components/ui/badge";
import { ErrorBox, PageHeader } from "@/components/ui/misc";
import { cn } from "@/lib/utils";
import { splitLines } from "@/lib/format";

const schema = z
  .object({
    name: z.string().max(100).optional(),
    primary_domain: z.string().min(1, "required"),
    sans: z.string().optional(),
    key_type: z.enum(["ec-256", "ec-384", "rsa-2048", "rsa-3072", "rsa-4096"]),
    ca_account_id: z.string().min(1, "required"),
    challenge_type: z.enum(["dns-01", "http-01-webroot", "http-01-standalone", "tls-alpn-01"]),
    dns_credential_id: z.string().optional(),
    webroot: z.string().optional(),
    auto_renew: z.boolean(),
    preferred_chain: z.string().optional(),
    dns_alias: z.string().optional(),
    dns_sleep: z.coerce.number().min(0).max(3600),
    always_new_key: z.boolean(),
    must_staple: z.boolean(),
    issue_now: z.boolean(),
  })
  .superRefine((v, ctx) => {
    const wildcard = [v.primary_domain, ...(v.sans ? splitLines(v.sans) : [])].some((d) => d.trim().startsWith("*."));
    if (wildcard && v.challenge_type !== "dns-01") {
      ctx.addIssue({ code: z.ZodIssueCode.custom, path: ["challenge_type"], message: "wildcard requires dns-01" });
    }
    if (v.challenge_type === "dns-01" && !v.dns_credential_id) {
      ctx.addIssue({ code: z.ZodIssueCode.custom, path: ["dns_credential_id"], message: "required" });
    }
    if (v.challenge_type === "http-01-webroot" && !v.webroot) {
      ctx.addIssue({ code: z.ZodIssueCode.custom, path: ["webroot"], message: "required" });
    }
  });

type FormValues = z.infer<typeof schema>;
type FormInput = z.input<typeof schema>;

const challenges: ChallengeType[] = ["dns-01", "http-01-webroot", "http-01-standalone", "tls-alpn-01"];
const stepFields: (keyof FormValues)[][] = [
  ["name", "key_type", "ca_account_id", "auto_renew"],
  ["primary_domain", "sans"],
  ["challenge_type", "dns_credential_id", "webroot"],
  ["preferred_chain", "dns_alias", "dns_sleep", "always_new_key", "must_staple"],
  [],
];

export function CertificateNewPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [step, setStep] = React.useState(0);
  const [error, setError] = React.useState("");
  const { data: caData } = useCA();
  const { data: dnsData } = useDNS();
  const { data: providerData } = useDNSProviders();
  const { data: info } = useInfo();
  const appliedDefaultCA = React.useRef(false);

  const {
    register,
    handleSubmit,
    watch,
    setValue,
    trigger,
    formState: { errors, isSubmitting },
  } = useForm<FormInput, unknown, FormValues>({
    resolver: zodResolver(schema),
    defaultValues: {
      name: "",
      primary_domain: "",
      sans: "",
      key_type: "ec-256",
      ca_account_id: "",
      challenge_type: "dns-01",
      dns_credential_id: "",
      webroot: "",
      auto_renew: true,
      preferred_chain: "",
      dns_alias: "",
      dns_sleep: 30,
      always_new_key: false,
      must_staple: false,
      issue_now: true,
    },
  });

  const values = watch();
  const challenge = values.challenge_type;
  const registrableCAs = (caData?.items ?? []).filter((c) => c.status !== "disabled");

  React.useEffect(() => {
    if (appliedDefaultCA.current || !info?.default_ca_id) return;
    if (!registrableCAs.some((c) => c.id === info.default_ca_id)) return;
    appliedDefaultCA.current = true;
    setValue("ca_account_id", info.default_ca_id);
  }, [info?.default_ca_id, registrableCAs, setValue]);
  const providerName = (id: string) => providerData?.items.find((p) => p.id === id)?.name ?? id;

  const submit = async (v: FormValues) => {
    setError("");
    const body: CertInput = {
      name: v.name || "",
      primary_domain: v.primary_domain,
      san: splitLines(v.sans || ""),
      key_type: v.key_type,
      ca_account_id: v.ca_account_id,
      challenge_type: v.challenge_type,
      dns_credential_id: v.challenge_type === "dns-01" ? v.dns_credential_id : undefined,
      webroot: v.challenge_type === "http-01-webroot" ? v.webroot : undefined,
      options: {
        preferred_chain: v.preferred_chain || undefined,
        dns_alias: v.dns_alias || undefined,
        dns_sleep: v.dns_sleep || 0,
        always_new_key: v.always_new_key,
        must_staple: v.must_staple,
      },
      auto_renew: v.auto_renew,
    };
    try {
      const cert = await request<Certificate>("/certificates", { method: "POST", body });
      if (v.issue_now) {
        await request<{ job_id: string }>(`/certificates/${cert.id}/issue`, {
          method: "POST",
          body: {},
          headers: { "Idempotency-Key": `web-issue-${cert.id}-${Date.now()}` },
        });
        toast.success(t("cert.issued"));
      } else {
        toast.success(t("cert.created"));
      }
      navigate(`/certificates/${cert.id}`);
    } catch (err) {
      setError(errorMessage(err));
    }
  };

  const next = async () => {
    const fields = stepFields[step];
    const ok = fields.length === 0 ? true : await trigger(fields, { shouldFocus: true });
    if (ok) setStep((s) => Math.min(s + 1, 4));
  };

  const stepNames = [t("cert.stepBasics"), t("cert.stepDomains"), t("cert.stepChallenge"), t("cert.stepAdvanced"), t("cert.stepReview")];

  return (
    <div className="mx-auto max-w-3xl">
      <PageHeader title={t("cert.new")} subtitle={t("cert.step", { current: step + 1, total: 5 })} />
      <div className="mb-6 flex items-center gap-2">
        {stepNames.map((name, i) => (
          <React.Fragment key={name}>
            <div
              className={cn(
                "flex h-7 w-7 shrink-0 items-center justify-center rounded-full border text-xs font-medium",
                i < step
                  ? "border-primary bg-primary text-primary-foreground"
                  : i === step
                    ? "border-primary text-primary"
                    : "border-border text-muted-foreground",
              )}
            >
              {i < step ? <Check className="h-3.5 w-3.5" /> : i + 1}
            </div>
            <span className={cn("hidden text-xs sm:block", i === step ? "text-foreground" : "text-muted-foreground")}>{name}</span>
            {i < 4 && <div className="h-px flex-1 bg-border" />}
          </React.Fragment>
        ))}
      </div>

      <form onSubmit={handleSubmit(submit)}>
        <Card>
          <CardHeader>
            <CardTitle>{stepNames[step]}</CardTitle>
          </CardHeader>
          <CardContent className="space-y-5">
            {error && <ErrorBox message={error} />}

            {step === 0 && (
              <>
                <Field label={t("common.name")} hint={t("common.optional")} error={errors.name?.message}>
                  <Input placeholder={values.primary_domain || "My site"} {...register("name")} />
                </Field>
                <Field label={t("cert.keyType")} error={errors.key_type?.message}>
                  <Select {...register("key_type")}>
                    {["ec-256", "ec-384", "rsa-2048", "rsa-3072", "rsa-4096"].map((k) => (
                      <option key={k} value={k}>
                        {k}
                      </option>
                    ))}
                  </Select>
                </Field>
                <Field
                  label={t("cert.ca")}
                  error={errors.ca_account_id?.message}
                  hint={!registrableCAs.length ? t("cert.noCA") : undefined}
                >
                  <Select {...register("ca_account_id")}>
                    <option value="">—</option>
                    {registrableCAs.map((ca) => (
                      <option key={ca.id} value={ca.id}>
                        {ca.name} ({ca.status})
                      </option>
                    ))}
                  </Select>
                </Field>
                <label className="flex items-center justify-between rounded-md border border-border p-3">
                  <span className="text-sm">{t("cert.autoRenew")}</span>
                  <Switch checked={values.auto_renew} onCheckedChange={(v) => setValue("auto_renew", v)} aria-label={t("cert.autoRenew")} />
                </label>
              </>
            )}

            {step === 1 && (
              <>
                <Field label={t("cert.domain")} error={errors.primary_domain?.message}>
                  <Input placeholder="example.com" autoCapitalize="none" {...register("primary_domain")} />
                </Field>
                <Field label={t("cert.sans")} hint={t("cert.sanHint")}>
                  <Textarea rows={4} placeholder={"www.example.com\napi.example.com"} {...register("sans")} />
                </Field>
              </>
            )}

            {step === 2 && (
              <>
                <Field label={t("cert.challenge")} error={errors.challenge_type?.message}>
                  <Select {...register("challenge_type")}>
                    {challenges.map((c) => (
                      <option key={c} value={c}>
                        {c}
                      </option>
                    ))}
                  </Select>
                  <p className="mt-1 text-xs text-muted-foreground">
                    {t(`cert.challengeHint.${challenge}` as const, { defaultValue: "" })}
                  </p>
                </Field>
                {challenge === "dns-01" && (
                  <Field label={t("cert.dnsCredential")} error={errors.dns_credential_id?.message} hint={!dnsData?.items.length ? t("cert.noDNS") : undefined}>
                    <Select {...register("dns_credential_id")}>
                      <option value="">—</option>
                      {(dnsData?.items ?? []).map((d) => (
                        <option key={d.id} value={d.id}>
                          {d.name} · {providerName(d.provider)}
                        </option>
                      ))}
                    </Select>
                  </Field>
                )}
                {challenge === "http-01-webroot" && (
                  <Field label={t("cert.webroot")} error={errors.webroot?.message}>
                    <Input placeholder="/var/www/html" {...register("webroot")} />
                  </Field>
                )}
              </>
            )}

            {step === 3 && (
              <>
                <Field label={t("cert.preferredChain")}>
                  <Input placeholder="ISRG Root X1" {...register("preferred_chain")} />
                </Field>
                <Field label={t("cert.dnsAlias")}>
                  <Input placeholder="_acme-challenge.example.com" {...register("dns_alias")} />
                </Field>
                <Field label={t("cert.dnsSleep")}>
                  <Input type="number" min={0} max={3600} {...register("dns_sleep")} />
                </Field>
                <label className="flex items-center justify-between rounded-md border border-border p-3">
                  <span className="text-sm">{t("cert.alwaysNewKey")}</span>
                  <Switch checked={values.always_new_key} onCheckedChange={(v) => setValue("always_new_key", v)} aria-label={t("cert.alwaysNewKey")} />
                </label>
                <label className="flex items-center justify-between rounded-md border border-border p-3">
                  <span className="text-sm">{t("cert.mustStaple")}</span>
                  <Switch checked={values.must_staple} onCheckedChange={(v) => setValue("must_staple", v)} aria-label={t("cert.mustStaple")} />
                </label>
              </>
            )}

            {step === 4 && (
              <div className="space-y-4">
                <dl className="grid gap-3 text-sm sm:grid-cols-2">
                  <div>
                    <dt className="text-muted-foreground">{t("cert.domain")}</dt>
                    <dd className="font-medium">{values.primary_domain}</dd>
                  </div>
                  <div>
                    <dt className="text-muted-foreground">{t("cert.sans")}</dt>
                    <dd className="font-medium">{splitLines(values.sans || "").join(", ") || "—"}</dd>
                  </div>
                  <div>
                    <dt className="text-muted-foreground">{t("cert.keyType")}</dt>
                    <dd className="font-medium">{values.key_type}</dd>
                  </div>
                  <div>
                    <dt className="text-muted-foreground">{t("cert.ca")}</dt>
                    <dd className="font-medium">{registrableCAs.find((c) => c.id === values.ca_account_id)?.name ?? "—"}</dd>
                  </div>
                  <div>
                    <dt className="text-muted-foreground">{t("cert.challenge")}</dt>
                    <dd>
                      <Badge variant="muted">{values.challenge_type}</Badge>
                    </dd>
                  </div>
                  <div>
                    <dt className="text-muted-foreground">{t("cert.autoRenew")}</dt>
                    <dd>{values.auto_renew ? t("common.yes") : t("common.no")}</dd>
                  </div>
                </dl>
                <label className="flex items-center justify-between rounded-md border border-border p-3">
                  <span className="text-sm">{t("cert.issue")}</span>
                  <Switch checked={values.issue_now} onCheckedChange={(v) => setValue("issue_now", v)} aria-label={t("cert.issue")} />
                </label>
              </div>
            )}
          </CardContent>
        </Card>

        <div className="mt-4 flex justify-between">
          <Button type="button" variant="outline" onClick={() => (step === 0 ? navigate(-1) : setStep((s) => s - 1))}>
            <ArrowLeft className="h-4 w-4" /> {t("common.back")}
          </Button>
          {step < 4 ? (
            <Button type="button" onClick={next}>
              {t("common.next")} <ArrowRight className="h-4 w-4" />
            </Button>
          ) : (
            <Button type="button" disabled={isSubmitting} onClick={handleSubmit(submit)}>
              {isSubmitting ? <Loader2 className="h-4 w-4 animate-spin" /> : <Check className="h-4 w-4" />}
              {t("common.create")}
            </Button>
          )}
        </div>
      </form>
    </div>
  );
}
