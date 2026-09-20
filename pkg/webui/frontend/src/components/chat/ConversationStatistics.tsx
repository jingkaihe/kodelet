import { ChevronDown } from 'lucide-react';
import { memo, useEffect, useState } from 'react';
import type { Conversation, Runner } from '../../types';
import { formatCompactRelativeTime, formatCost, formatRunnerStatus } from '../../utils';

interface ConversationStatisticsProps {
  conversation: Conversation | null;
  conversationId: string | null;
  runner?: Runner;
  runnerId: string;
  environmentProfile: string;
}

const ConversationStatistics = ({
  conversation,
  conversationId,
  runner,
  runnerId,
  environmentProfile,
}: ConversationStatisticsProps) => {
  const [, setStatusTick] = useState(0);
  useEffect(() => {
    const interval = window.setInterval(() => setStatusTick((current) => current + 1), 30000);
    return () => window.clearInterval(interval);
  }, []);

  if (!conversation) return null;

  const groups: Array<{ label: string; value: string }> = [];
  const details: Array<{ label: string; value: string }> = [];
  if (runnerId) {
    details.push(
      { label: 'Runner', value: runner?.displayName || runner?.workspace.name || runnerId },
      { label: 'Status', value: formatRunnerStatus(runner) }
    );
    if (environmentProfile) {
      details.push({ label: 'Runner profile', value: environmentProfile });
    }
  }

  const usage = conversation.usage;
  const compactNumber = Intl.NumberFormat('en-US', {
    notation: 'compact',
    maximumFractionDigits: 1,
  });
  const exactNumber = Intl.NumberFormat('en-US');
  const usageParts: string[] = [];
  if (
    usage?.currentContextWindow !== undefined &&
    usage.maxContextWindow &&
    usage.maxContextWindow > 0
  ) {
    const percentage = Math.max(
      0,
      Math.min(100, Math.round((usage.currentContextWindow / usage.maxContextWindow) * 100))
    );
    usageParts.push(`ctx ${percentage}%`);
    details.push({
      label: 'Context window',
      value: `${exactNumber.format(usage.currentContextWindow)} / ${exactNumber.format(usage.maxContextWindow)} tokens (${percentage}%)`,
    });
  }
  for (const [label, shortLabel, tokens] of [
    ['Input tokens', 'in', usage?.inputTokens],
    ['Output tokens', 'out', usage?.outputTokens],
    ['Cache read tokens', 'cache', usage?.cacheReadInputTokens],
    ['Cache write tokens', 'cache write', usage?.cacheCreationInputTokens],
  ] as const) {
    if (tokens && tokens > 0) {
      usageParts.push(`${shortLabel} ${compactNumber.format(tokens)}`);
      details.push({ label, value: exactNumber.format(tokens) });
    }
  }
  if (usageParts.length > 0) {
    groups.push({ label: 'Usage', value: usageParts.join(' · ') });
  }

  const totalCost =
    (usage?.inputCost || 0) +
    (usage?.outputCost || 0) +
    (usage?.cacheCreationCost || 0) +
    (usage?.cacheReadCost || 0);
  const costParts = [
    totalCost > 0 && totalCost < 0.01
      ? '<$0.01'
      : Intl.NumberFormat('en-US', { style: 'currency', currency: 'USD' }).format(totalCost),
  ];
  details.push({ label: 'Total cost', value: formatCost(usage) });
  if (conversation.updatedAt) {
    costParts.push(formatCompactRelativeTime(conversation.updatedAt));
  }
  groups.push({ label: 'Cost and time', value: costParts.join(' · ') });

  return (
    <div className="transcript-meta-strip-shell">
      <div className="mx-auto w-full max-w-5xl px-3 sm:px-4 md:px-8">
        <details className="transcript-meta" key={conversationId || 'new-chat'}>
          <summary
            aria-label="Conversation statistics"
            className="transcript-meta-strip"
            data-testid="transcript-meta-strip"
            title="Show detailed conversation statistics"
          >
            <span className="transcript-meta-groups">
              {groups.map(({ label, value }) => (
                <span key={label}>{value}</span>
              ))}
            </span>
            <ChevronDown aria-hidden="true" className="transcript-meta-chevron" strokeWidth={1.6} />
          </summary>
          <dl className="transcript-meta-details" data-testid="transcript-meta-details">
            {details.map(({ label, value }) => (
              <div key={label}>
                <dt>{label}</dt>
                <dd>{value}</dd>
              </div>
            ))}
          </dl>
        </details>
      </div>
    </div>
  );
};

export default memo(ConversationStatistics);
