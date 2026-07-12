
import { lazy, Suspense } from 'react';
import { BrowserRouter as Router, Routes, Route } from 'react-router-dom';
import ArcadeGames from './components/games/ArcadeGames';

const ChatPage = lazy(() => import('./pages/ChatPage'));
const TerminalPage = lazy(() => import('./pages/TerminalPage'));

function App() {
  return (
    <Router future={{
      v7_startTransition: true,
      v7_relativeSplatPath: true
    }}>
      <div className="min-h-screen">
        <ArcadeGames />
        <Suspense fallback={<div className="app-loading" role="status">Loading Kodelet…</div>}>
          <Routes>
            <Route path="/" element={<ChatPage />} />
            <Route path="/c/:id" element={<ChatPage />} />
            <Route path="/terminal" element={<TerminalPage />} />
          </Routes>
        </Suspense>
      </div>
    </Router>
  );
}

export default App;
