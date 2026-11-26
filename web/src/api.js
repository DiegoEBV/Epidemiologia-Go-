export class APIError extends Error {
  constructor(status, message) {
    super(message);
    this.status = status;
  }
}

export async function request(path, { token, body, signal } = {}) {
  const headers = { Accept: 'application/json' };
  if (token) headers.Authorization = 'Bearer ' + token;
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  const res = await fetch(path, {
    method: body === undefined ? 'GET' : 'POST',
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
    signal,
    credentials: 'omit',
  });
  const data = await res.json().catch(() => null);
  if (!res.ok) throw new APIError(res.status, data?.error || 'La API respondió con HTTP ' + res.status);
  if (data === null) throw new Error('La API devolvió una respuesta inesperada.');
  return data;
}

export function stream(token) {
  const protocol = location.protocol === 'https:' ? 'wss:' : 'ws:';
  return new WebSocket(protocol + '//' + location.host + '/ws', ['epidemiologia', 'jwt.' + token]);
}
