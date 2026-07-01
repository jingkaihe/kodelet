import TerminalModal from '../components/workspace/TerminalModal';

const TerminalPage = () => {
  const params = new URLSearchParams(window.location.search);
  const cwdLabel = params.get('cwd') ?? '';

  return (
    <main className="terminal-popout-page" data-testid="terminal-popout-page">
      <TerminalModal
        cwdLabel={cwdLabel}
        open
        onClose={() => window.close()}
        showPopOut={false}
      />
    </main>
  );
};

export default TerminalPage;
