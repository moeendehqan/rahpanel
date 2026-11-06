import {expect,it} from 'vitest';
import {userPayload,online} from './helpers';
it('rejects negative quotas instead of silently producing unlimited accounts',()=>{expect(()=>userPayload('ali','-1','2027-01-01','default')).toThrow()});
it('creates UTC expiry and exact byte quotas',()=>{const u=userPayload('ali','2','2027-01-01','default');expect(u.quota_bytes).toBe(2147483648);expect(u.expires_at).toBe('2027-01-01T23:59:59.000Z')});
it('does not call a stale or disabled node online',()=>{expect(online({enabled:true,ready:true,last_seen:'2026-01-01T00:00:00Z'},Date.parse('2026-01-01T00:05:00Z'))).toBe(false);expect(online({enabled:false,ready:true,last_seen:new Date().toISOString()})).toBe(false)});
