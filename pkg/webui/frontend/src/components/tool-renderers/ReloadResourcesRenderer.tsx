import type React from 'react';

// The reload itself happens after the tool batch; its outcome is a transcript marker.
export const RELOAD_RESOURCES_DETAIL = 'Resources reload after the current tool batch.';

const ReloadResourcesRenderer: React.FC = () => (
  <div className="quiet-tool-detail">
    <div className="quiet-tool-line">{RELOAD_RESOURCES_DETAIL}</div>
  </div>
);

export default ReloadResourcesRenderer;
