
import { BrowserRouter as Router, Routes, Route } from 'react-router-dom';
import ArcadeGames from './components/games/ArcadeGames';
import ChatPage from './pages/ChatPage';
import TerminalPage from './pages/TerminalPage';

function App() {
  return (
    <Router future={{
      v7_startTransition: true,
      v7_relativeSplatPath: true
    }}>
      <div className="min-h-screen">
        <ArcadeGames />
        <Routes>
          <Route path="/" element={<ChatPage />} />
          <Route path="/c/:id" element={<ChatPage />} />
          <Route path="/terminal" element={<TerminalPage />} />
        </Routes>
      </div>
    </Router>
  );
}

export default App;
