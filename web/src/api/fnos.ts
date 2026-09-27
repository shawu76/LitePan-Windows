import { http } from "./client";

export interface FnosConfig {
  enabled: boolean;
  name: string;
  fnos_url: string;
  proxy_port: string;
  strm_path_maps: string;
  direct_strm_clients: string;
  strm_dir: string;
  proxy_url: string;
  running: boolean;
  last_error?: string;
  admin_username?: string;
  management_ready: boolean;
}

export interface FnosConfigUpdate {
  enabled: boolean;
  name: string;
  fnos_url: string;
  proxy_port: string;
  strm_path_maps: string;
  direct_strm_clients: string;
}

export function fetchFnosConfig() {
  return http.get<FnosConfig>("/admin/fnos/config");
}

export function saveFnosConfig(values: FnosConfigUpdate) {
  return http.put<FnosConfig>("/admin/fnos/config", values);
}

export function testFnosConfig(values: FnosConfigUpdate) {
  return http.post<{ ok: boolean }>("/admin/fnos/test", values);
}

export interface FnosLibrary {
  id: string;
  name: string;
}

export function saveFnosManagement(values: { username: string; password: string }) {
  return http.put<FnosConfig>("/admin/fnos/management", values);
}

export function testFnosManagement(values: { username: string; password: string }) {
  return http.post<{ ok: boolean }>("/admin/fnos/management/test", values);
}

export function fetchFnosLibraries() {
  return http.get<FnosLibrary[]>("/admin/fnos/libraries");
}
