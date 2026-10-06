export function shareURLWithPassword(rawURL: string, rawPassword?: string) {
  const url = rawURL.trim();
  const password = rawPassword?.trim();
  if (!url || !password) return url;

  try {
    const parsed = new URL(url);
    parsed.searchParams.set("pwd", password);
    return parsed.toString();
  } catch {
    const separator = url.includes("?") ? "&" : "?";
    return `${url}${separator}pwd=${encodeURIComponent(password)}`;
  }
}

export function shareTrafficSwitch(guest: boolean, freeUser: boolean) {
  if (guest && freeUser) return 4;
  if (guest) return 2;
  if (freeUser) return 3;
  return 1;
}
