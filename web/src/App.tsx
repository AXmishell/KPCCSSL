import { Navigate, Route, Routes, useLocation } from "react-router-dom";
import { useAuth } from "@/auth";
import { AppLayout } from "@/components/layout";
import { Spinner } from "@/components/ui/misc";
import { DashboardPage } from "@/pages/dashboard";
import { CertificatesPage } from "@/pages/certificates";
import { CertificateNewPage } from "@/pages/certificate-new";
import { CertificateDetailPage } from "@/pages/certificate-detail";
import { JobsPage } from "@/pages/jobs";
import { JobDetailPage } from "@/pages/job-detail";
import { CAAccountsPage } from "@/pages/ca-accounts";
import { DNSCredentialsPage } from "@/pages/dns-credentials";
import { DeploymentsPage } from "@/pages/deployments";
import { DeploymentCredentialsPage } from "@/pages/deployment-credentials";
import { WorkflowsPage } from "@/pages/workflows";
import { NotificationsPage } from "@/pages/notifications";
import { AuditPage } from "@/pages/audit";
import { SettingsPage } from "@/pages/settings";
import { ProfilePage } from "@/pages/profile";
import { LoginPage } from "@/pages/login";
import { SetupPage } from "@/pages/setup";
import { ForgotPasswordPage } from "@/pages/forgot-password";
import { ResetPasswordPage } from "@/pages/reset-password";
import { ConfirmEmailPage } from "@/pages/confirm-email";

const publicPaths = ["/login", "/setup", "/forgot-password", "/reset-password", "/confirm-email"];

function Splash() {
  return (
    <div className="flex min-h-full items-center justify-center">
      <Spinner className="h-8 w-8 text-primary" />
    </div>
  );
}

export function App() {
  const { user, loading, setupRequired } = useAuth();
  const location = useLocation();

  if (loading) return <Splash />;
  if (setupRequired && location.pathname !== "/setup") return <Navigate to="/setup" replace />;
  if (!user && !publicPaths.includes(location.pathname)) {
    return <Navigate to="/login" replace />;
  }
  if (user && (location.pathname === "/login" || location.pathname === "/setup")) {
    return <Navigate to="/" replace />;
  }

  return (
    <Routes>
      <Route path="/login" element={<LoginPage />} />
      <Route path="/setup" element={<SetupPage />} />
      <Route path="/forgot-password" element={<ForgotPasswordPage />} />
      <Route path="/reset-password" element={<ResetPasswordPage />} />
      <Route path="/confirm-email" element={<ConfirmEmailPage />} />
      <Route element={<AppLayout />}>
        <Route path="/" element={<DashboardPage />} />
        <Route path="/certificates" element={<CertificatesPage />} />
        <Route path="/certificates/new" element={<CertificateNewPage />} />
        <Route path="/certificates/:id" element={<CertificateDetailPage />} />
        <Route path="/jobs" element={<JobsPage />} />
        <Route path="/jobs/:id" element={<JobDetailPage />} />
        <Route path="/ca-accounts" element={<CAAccountsPage />} />
        <Route path="/dns-credentials" element={<DNSCredentialsPage />} />
        <Route path="/deployments" element={<DeploymentsPage />} />
        <Route path="/deployment-credentials" element={<DeploymentCredentialsPage />} />
        <Route path="/workflows" element={<WorkflowsPage />} />
        <Route path="/notifications" element={<NotificationsPage />} />
        <Route path="/audit" element={<AuditPage />} />
        <Route path="/settings" element={<SettingsPage />} />
        <Route path="/profile" element={<ProfilePage />} />
      </Route>
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  );
}
