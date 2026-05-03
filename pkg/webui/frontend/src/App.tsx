
import { BrowserRouter as Router, Routes, Route } from 'react-router-dom';
import KonamiPongEgg from './components/easter-eggs/KonamiPongEgg';
import ChatPage from './pages/ChatPage';

function App() {
  return (
    <Router future={{
      v7_startTransition: true,
      v7_relativeSplatPath: true
    }}>
      <div className="min-h-screen">
        <KonamiPongEgg />
        <Routes>
          <Route path="/" element={<ChatPage />} />
          <Route path="/c/:id" element={<ChatPage />} />
        </Routes>
      </div>
    </Router>
  );
}

export default App;
