export type User={id:string;name:string;enabled:boolean;expires_at:string;quota_bytes:number;used_bytes:number;group_id:string;token:string};
export type NodePorts={reality:number;websocket:number;trojan:number;stats:number};
export type Node={id:string;name:string;host:string;domain:string;group_id:string;enabled:boolean;ready:boolean;verified:string[];last_seen:string;last_error:string;applied_version:string;shared_host:boolean;ports:NodePorts;ports_locked:boolean};
export type Group={id:string;name:string};
export type Job={id:string;node_id:string;status:string;step:string;error:string;updated_at:string};
export type Settings={domain:string;zone_id:string;cloudflare_token:string;email:string;agent_url:string;public_url:string;reality_target:string;reality_sni:string;acme_url:string};
export type Audit={admin_id:string;action:string;target:string;created_at:string};
