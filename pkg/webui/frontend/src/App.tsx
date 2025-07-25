
import { BrowserRouter as Router, Routes, Route } from 'react-router-dom';
import ConversationListPage from './pages/ConversationListPage';
import ConversationViewPage from './pages/ConversationViewPage';

function App() {
  return (
    <Router future={{
      v7_startTransition: true,
      v7_relativeSplatPath: true
    }}>
      <div className="min-h-screen bg-base-100">
        <Routes>
          <Route path="/" element={<ConversationListPage />} />
          <Route path="/c/:id" element={<ConversationViewPage />} />
        </Routes>
      </div>
    </Router>
  );
}

export default App;