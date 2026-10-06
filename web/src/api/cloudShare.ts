import { http } from "./client";
import type {
  CloudShareCapabilities,
  CloudShareItem,
  CloudShareKind,
  CloudSharePage,
  CreateCloudSharePayload,
  UpdateCloudSharePayload,
  CancelCloudSharesPayload,
} from "@/types/cloud-share";

export const cloudShareApi = {
  capabilities(accountId: number) {
    return http.get<CloudShareCapabilities>("/files/shares/capabilities", { account_id: accountId });
  },
  create(payload: CreateCloudSharePayload) {
    return http.post<CloudShareItem>("/files/shares/", payload);
  },
  list(accountId: number, kind: CloudShareKind, cursor = "", limit = 50) {
    return http.get<CloudSharePage>("/files/shares/", { account_id: accountId, kind, cursor, limit });
  },
  update(payload: UpdateCloudSharePayload) {
    return http.put<void>("/files/shares/", payload);
  },
  cancel(payload: CancelCloudSharesPayload) {
    return http.post<void>("/files/shares/cancel", payload);
  },
};
