export type CloudShareKind = "free" | "paid";

export interface CloudShareCapabilities {
  supported: boolean;
  supports_free: boolean;
  supports_paid: boolean;
  supports_manage: boolean;
  supports_password: boolean;
  supports_traffic: boolean;
  supports_cancel: boolean;
  max_items: number;
  expire_days: number[];
}

export interface CloudShareItem {
  id: string;
  kind: CloudShareKind;
  key: string;
  url: string;
  name: string;
  expiration: string;
  expired: boolean;
  password?: string;
  pay_amount?: number;
  income?: number;
  order_count?: number;
  traffic_switch: number;
  traffic_limit_switch: number;
  traffic_limit: number;
  used_bytes: number;
  preview_count: number;
  save_count: number;
  download_count: number;
}

export interface CloudSharePage {
  items: CloudShareItem[];
  next_cursor: string;
}

export interface CreateCloudSharePayload {
  account_id: number;
  kind: CloudShareKind;
  name: string;
  file_ids: string[];
  expire_days: number;
  password?: string;
  pay_amount?: number;
  reward_enabled?: boolean;
  resource_desc?: string;
  traffic_switch: number;
  traffic_limit_switch: number;
  traffic_limit: number;
}

export interface UpdateCloudSharePayload {
  account_id: number;
  kind: CloudShareKind;
  share_ids: string[];
  traffic_switch: number;
  traffic_limit_switch: number;
  traffic_limit: number;
}

export interface CancelCloudSharesPayload {
  account_id: number;
  share_ids: string[];
}
