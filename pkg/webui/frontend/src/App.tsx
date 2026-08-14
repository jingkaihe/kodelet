
import { lazy, Suspense, useEffect, useRef } from 'react';
import { BrowserRouter as Router, Routes, Route } from 'react-router';

const ChatPage = lazy(() => import('./pages/ChatPage'));
const TerminalPage = lazy(() => import('./pages/TerminalPage'));
const UserLoginPage = lazy(() => import('./pages/UserLoginPage'));
const RunnerEnrollmentPage = lazy(() => import('./pages/RunnerEnrollmentPage'));
const SignedOutPage = lazy(() => import('./pages/SignedOutPage'));

function App() {
  const appRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    const app = appRef.current;
    if (!app) {
      return undefined;
    }

    const viewport = window.visualViewport;
    const updateViewportBounds = () => {
      const followsVisualViewport = Math.abs((viewport?.scale ?? 1) - 1) < 0.01;
      app.style.setProperty(
        '--app-viewport-top',
        `${followsVisualViewport ? viewport?.offsetTop ?? 0 : 0}px`
      );
      app.style.setProperty(
        '--app-viewport-left',
        `${followsVisualViewport ? viewport?.offsetLeft ?? 0 : 0}px`
      );
      app.style.setProperty(
        '--app-viewport-width',
        `${followsVisualViewport ? viewport?.width ?? window.innerWidth : window.innerWidth}px`
      );
      app.style.setProperty(
        '--app-viewport-height',
        `${followsVisualViewport ? viewport?.height ?? window.innerHeight : window.innerHeight}px`
      );
    };

    updateViewportBounds();
    window.addEventListener('resize', updateViewportBounds);
    viewport?.addEventListener('resize', updateViewportBounds);
    viewport?.addEventListener('scroll', updateViewportBounds);

    return () => {
      window.removeEventListener('resize', updateViewportBounds);
      viewport?.removeEventListener('resize', updateViewportBounds);
      viewport?.removeEventListener('scroll', updateViewportBounds);
    };
  }, []);

  return (
    <Router>
      <div className="app-viewport h-full min-h-0 overflow-hidden" ref={appRef}>
        <Suspense fallback={<div className="app-loading" role="status">Loading Kodelet…</div>}>
          <Routes>
            <Route path="/" element={<ChatPage />} />
            <Route path="/c/:id" element={<ChatPage />} />
            <Route path="/terminal" element={<TerminalPage />} />
            <Route path="/auth/device" element={<UserLoginPage />} />
            <Route path="/auth/signed-out" element={<SignedOutPage />} />
            <Route path="/runner/enroll" element={<RunnerEnrollmentPage />} />
          </Routes>
        </Suspense>
      </div>
    </Router>
  );
}

export default App;
