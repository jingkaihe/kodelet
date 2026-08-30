import { ChevronDown } from 'lucide-react';
import { useState, type CSSProperties } from 'react';
import type { UIFrameLine, UIStyle, UIStyledSpan, UIWidgetEvent } from '../../types';

interface ExtensionWidgetsProps {
  placement: 'aboveComposer' | 'belowComposer';
  widgets: UIWidgetEvent[];
}

const ANSI_COLORS: Record<string, string> = {
  black: '#111827',
  red: '#dc2626',
  green: '#16a34a',
  yellow: '#ca8a04',
  blue: '#2563eb',
  magenta: '#c026d3',
  cyan: '#0891b2',
  white: '#f9fafb',
  gray: '#6b7280',
  grey: '#6b7280',
  brightBlack: '#4b5563',
  brightRed: '#ef4444',
  brightGreen: '#22c55e',
  brightYellow: '#eab308',
  brightBlue: '#3b82f6',
  brightMagenta: '#d946ef',
  brightCyan: '#06b6d4',
  brightWhite: '#ffffff',
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
    fontWeight: style.bold ? 700 : undefined,
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
  const [collapsedWidgets, setCollapsedWidgets] = useState<Record<string, boolean>>({});
  const placedWidgets = widgets
    .filter((widget) => (widget.placement || 'aboveComposer') === placement)
    .slice(0, MAX_RENDERED_WIDGETS);
  if (placedWidgets.length === 0) {
    return null;
  }

  return (
    <section
      aria-label="Extension status"
      className="extension-widgets mx-auto flex w-full max-w-5xl flex-col gap-1.5 px-3 sm:px-4 md:px-8"
      data-placement={placement}
      data-testid={`extension-widgets-${placement}`}
    >
      {placedWidgets.map((widget) => {
        const lines = (Array.isArray(widget.frame?.lines) ? widget.frame.lines : []).slice(
          0,
          MAX_RENDERED_WIDGET_LINES
        );
        const headerLine = lines[0] ?? widget.id;
        const collapsed = collapsedWidgets[widget.key] === true;

        return (
          <div
            aria-label={`${widget.extension_id} status`}
            className="extension-widget-frame"
            data-testid={`extension-widget-${widget.key}`}
            key={widget.key}
          >
            <button
              aria-expanded={!collapsed}
              className="extension-widget-toggle"
              onClick={() =>
                setCollapsedWidgets((current) => ({
                  ...current,
                  [widget.key]: current[widget.key] !== true,
                }))
              }
              type="button"
            >
              <div
                className="extension-widget-line extension-widget-line-header"
              >
                {spansForLine(headerLine).map((span, spanIndex) => (
                  <span key={spanIndex} style={spanStyle(span.style)}>
                    {span.text}
                  </span>
                ))}
              </div>
              <ChevronDown aria-hidden="true" className="extension-widget-chevron" />
            </button>
            {collapsed ? null : (
              <div className="extension-widget-content">
                {lines.slice(1).map((line, lineIndex) => (
                  <div
                    className="extension-widget-line"
                    key={`${widget.frame.sequence}-${lineIndex + 1}`}
                  >
                    {spansForLine(line).map((span, spanIndex) => (
                      <span key={spanIndex} style={spanStyle(span.style)}>
                        {span.text}
                      </span>
                    ))}
                  </div>
                ))}
              </div>
            )}
          </div>
        );
      })}
    </section>
  );
};

export default ExtensionWidgets;
