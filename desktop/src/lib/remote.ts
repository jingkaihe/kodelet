export function normalizeRemoteServerURL(input: string): string {
  const trimmed = input.trim();
  if (!trimmed) {
    throw new Error('Remote server URL cannot be empty');
  }

  const candidate = withDefaultProtocol(trimmed);

  let parsed: URL;
  try {
    parsed = new URL(candidate);
  } catch {
    throw new Error(`Invalid remote server URL: ${trimmed}`);
  }

  if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
    throw new Error('Remote server URL must use http:// or https://');
  }

  if (parsed.pathname && parsed.pathname !== '/') {
    throw new Error('Remote server URL must point to the root of a kodelet serve instance');
  }

  if (!parsed.hostname) {
    throw new Error('Remote server URL must include a hostname');
  }

  const token = parsed.searchParams.get('token')?.trim() || '';

  parsed.pathname = '';
  parsed.search = '';
  parsed.hash = '';

  if (token) {
    parsed.searchParams.set('token', token);
  }

  return parsed.toString().replace(/\/(?=\?)/, '').replace(/\/$/, '');
}

export function buildRemoteServerURL(baseUrl: string, path: string): string {
  const parsedBaseURL = new URL(baseUrl);
  const endpoint = new URL(path, parsedBaseURL.origin);
  const token = parsedBaseURL.searchParams.get('token');

  if (token) {
    endpoint.searchParams.set('token', token);
  }

  return endpoint.toString();
}

export function removeRemoteServerToken(remoteUrl: string): string {
  const parsed = new URL(remoteUrl);
  parsed.searchParams.delete('token');
  return parsed.toString().replace(/\/$/, '');
}

export function getRemoteDisplayLabel(remoteUrl: string): string {
  try {
    return new URL(remoteUrl).host;
  } catch {
    return remoteUrl;
  }
}

function withDefaultProtocol(input: string): string {
  if (input.includes('://')) {
    return input;
  }

  if (looksLikeLocalAddress(input) || input.includes(':')) {
    return `http://${input}`;
  }

  return `https://${input}`;
}

function looksLikeLocalAddress(input: string): boolean {
  return /^(localhost|127\.0\.0\.1|0\.0\.0\.0|\[::1\])(?:[:/]|$)/i.test(input);
}
