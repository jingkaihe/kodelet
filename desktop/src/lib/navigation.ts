const allowedExternalProtocols = new Set(['http:', 'https:']);

export function canOpenExternalURL(url: string): boolean {
  try {
    return allowedExternalProtocols.has(new URL(url).protocol);
  } catch {
    return false;
  }
}
