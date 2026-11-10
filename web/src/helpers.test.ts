import {expect,it} from 'vitest';
import {userPayload,online,persianDate,expiryFromPersianDate,expiryFromDays} from './helpers';
it('rejects negative quotas instead of silently producing unlimited accounts',()=>{expect(()=>userPayload('ali','-1','2027-01-01T23:59:59.000Z','default')).toThrow()});
it('saves a Persian date through the end of the day in Tehran and calculates exact byte quotas',()=>{
  const u=userPayload('ali','2',expiryFromPersianDate('۱۴۰۳/۱۲/۳۰'),'default');
  expect(u.quota_bytes).toBe(2147483648);
  expect(u.expires_at).toBe('2025-03-20T20:29:59.000Z');
  expect(persianDate(new Date(u.expires_at))).toBe('1403/12/30');
  expect(persianDate(new Date(Date.parse(u.expires_at)+1000))).toBe('1404/01/01');
  expect(()=>expiryFromPersianDate('۱۴۰۴/۱۲/۳۰')).toThrow();
});
it('uses the Tehran offset in effect at the end of the selected day',()=>{
  expect(expiryFromPersianDate('1400/01/01')).toBe('2021-03-21T20:29:59.000Z');
  expect(expiryFromPersianDate('1400/04/10')).toBe('2021-07-01T19:29:59.000Z');
});
it('accepts only whole positive days and counts from the current instant',()=>{
  const now=Date.parse('2025-03-20T14:30:00Z');
  expect(expiryFromDays('٣٠',now)).toBe('2025-04-19T14:30:00.000Z');
  for(const days of ['','0','-1','1.5','1e2'])expect(expiryFromDays(days,now)).toBeUndefined();
});
it('does not call a stale or disabled node online',()=>{expect(online({enabled:true,ready:true,last_seen:'2026-01-01T00:00:00Z'},Date.parse('2026-01-01T00:05:00Z'))).toBe(false);expect(online({enabled:false,ready:true,last_seen:new Date().toISOString()})).toBe(false)});
