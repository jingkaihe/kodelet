import mustache from "mustache";

export type TemplateView = Record<string, unknown>;

export interface RenderTemplateOptions {
  partials?: Record<string, string>;
}

export function renderTemplate(template: string, view: TemplateView, options: RenderTemplateOptions = {}): string {
  return mustache.render(template, view, options.partials);
}
