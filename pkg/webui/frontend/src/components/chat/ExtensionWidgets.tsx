import { ChevronDown } from 'lucide-react';
import { type CSSProperties, useId, useState } from 'react';
import type { UIFrameLine, UIStyle, UIStyledSpan, UIWidgetEvent } from '../../types';

interface ExtensionWidgetsProps {
  placement: 'aboveComposer' | 'belowComposer';
  widgets: UIWidgetEvent[];
}

const ANSI_COLORS: Record<string, string> = {
  black: 'var(--tui-text)',
  red: 'var(--tui-red)',
  green: 'var(--tui-green)',
  yellow: 'var(--tui-yellow)',
  blue: 'var(--tui-blue)',
  magenta: 'var(--tui-mauve)',
  cyan: 'var(--tui-teal)',
  white: 'var(--kodelet-light)',
  gray: 'var(--tui-muted)',
  grey: 'var(--tui-muted)',
  brightBlack: 'var(--tui-subtext)',
  brightRed: 'var(--tui-red)',
  brightGreen: 'var(--tui-green)',
  brightYellow: 'var(--tui-yellow)',
  brightBlue: 'var(--tui-blue)',
  brightMagenta: 'var(--tui-mauve)',
  brightCyan: 'var(--tui-teal)',
  brightWhite: 'var(--kodelet-light)',
};

const MAX_RENDERED_WIDGET_LINES = 64;
const MAX_RENDERED_WIDGET_SPANS = 128;
const MAX_RENDERED_WIDGET_TEXT = 4096;
const MAX_RENDERED_WIDGETS = 32;

const safeColor = (color: string | undefined): string | undefined => {
  if (!color) {
    return undefined;
  }
  if (/^#[0-9a-f]{3,8}$/i.test(color)) {
    return color;
  }
  return ANSI_COLORS[color];
};

const spanStyle = (style: UIStyle | undefined): CSSProperties => {
  if (!style) {
    return {};
  }

  const foreground = safeColor(style.foreground);
  const background = safeColor(style.background);
  return {
    color: style.reverse ? background : foreground,
    backgroundColor: style.reverse ? foreground : background,
    fontWeight: style.bold ? 600 : undefined,
    fontStyle: style.italic ? 'italic' : undefined,
    opacity: style.dim ? 0.65 : undefined,
    textDecoration:
      [style.underline ? 'underline' : '', style.strikethrough ? 'line-through' : '']
        .filter(Boolean)
        .join(' ') || undefined,
  };
};

const boundedText = (text: unknown): string => {
  const value = typeof text === 'string' ? text : String(text ?? '');
  return value.length > MAX_RENDERED_WIDGET_TEXT
    ? `${value.slice(0, MAX_RENDERED_WIDGET_TEXT)}…`
    : value;
};

const spansForLine = (line: UIFrameLine): UIStyledSpan[] => {
  if (typeof line === 'string') {
    return [{ text: boundedText(line) }];
  }
  if (!line || !Array.isArray(line.spans)) {
    return [];
  }
  return line.spans.slice(0, MAX_RENDERED_WIDGET_SPANS).map((span) => ({
    ...span,
    text: boundedText(span?.text),
  }));
};

const ExtensionWidgets = ({ placement, widgets }: ExtensionWidgetsProps) => {
  const id = useId();
  const [expandedWidgets, setExpandedWidgets] = useState<Record<string, boolean>>({});
  const placedWidgets = widgets
    .filter((widget) => (widget.placement || 'aboveComposer') === placement)
    .slice(0, MAX_RENDERED_WIDGETS);
  if (placedWidgets.length === 0) {
    return null;
  }

  return (
    <section
      aria-label="Extension status"
      className="extension-widgets mx-auto flex w-full max-w-5xl flex-col gap-1 px-3 sm:px-4 md:px-8"
      data-placement={placement}
      data-testid={`extension-widgets-${placement}`}
    >
      {placedWidgets.map((widget) => {
        const lines = (Array.isArray(widget.frame?.lines) ? widget.frame.lines : []).slice(
          0,
          MAX_RENDERED_WIDGET_LINES
        );
        const headerLine = lines[0] ?? widget.id;
        const hasContent = lines.length > 1;
        const expanded = expandedWidgets[widget.key] === true;
        const contentId = `${id}-${encodeURIComponent(widget.key)}`;
        const header = (
          <span className="extension-widget-line extension-widget-line-header">
            {spansForLine(headerLine).map((span, spanIndex) => (
              // biome-ignore lint/suspicious/noArrayIndexKey: These stateless text runs represent positional cells in a terminal frame and have no persistent IDs.
              <span key={spanIndex} style={spanStyle(span.style)}>
                {span.text}
              </span>
            ))}
          </span>
        );

        return (
          <section
            aria-label={`${widget.extension_id} status`}
            className="extension-widget-frame"
            data-testid={`extension-widget-${widget.key}`}
            key={widget.key}
          >
            {hasContent ? (
              <button
                aria-controls={contentId}
                aria-expanded={expanded}
                className="extension-widget-toggle"
                onClick={() =>
                  setExpandedWidgets((current) => ({
                    ...current,
                    [widget.key]: current[widget.key] !== true,
                  }))
                }
                type="button"
              >
                <ChevronDown
                  aria-hidden="true"
                  className="extension-widget-chevron"
                  strokeWidth={1.6}
                />
                {header}
              </button>
            ) : (
              <div className="extension-widget-heading">{header}</div>
            )}
            {hasContent && expanded ? (
              <div className="extension-widget-content" id={contentId}>
                {lines.slice(1).map((line, lineIndex) => (
                  <div
                    className="extension-widget-line"
                    key={`${widget.frame.sequence}-${lineIndex + 1}`}
                  >
                    {spansForLine(line).map((span, spanIndex) => (
                      // biome-ignore lint/suspicious/noArrayIndexKey: These stateless text runs represent positional cells in a terminal frame and have no persistent IDs.
                      <span key={spanIndex} style={spanStyle(span.style)}>
                        {span.text}
                      </span>
                    ))}
                  </div>
                ))}
              </div>
            ) : null}
          </section>
        );
      })}
    </section>
  );
};

export default ExtensionWidgets;
