import { LogIn } from 'lucide-react';
import { AuthNotice, AuthPageShell } from '../components/auth/AuthPageShell';

export default function SignedOutPage() {
  return (
    <AuthPageShell description="Your Kodelet session has ended." title="Signed out">
      <AuthNotice tone="info">
        Your identity provider may still be signed in.
      </AuthNotice>
      <div className="auth-completion-actions">
        <a className="auth-primary-button gap-2" href="/">
          <LogIn aria-hidden="true" size={16} strokeWidth={1.9} />
          Sign in
        </a>
      </div>
    </AuthPageShell>
  );
}
