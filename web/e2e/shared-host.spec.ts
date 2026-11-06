import {test,expect} from '@playwright/test';

test('shared panel host uses configurable ports and blocks reserved listeners before SSH',async({page})=>{
 let fingerprintCalls=0;
 await page.route('**/api/v1/**',async route=>{
  const path=new URL(route.request().url()).pathname.replace('/api/v1','');
  if(path==='/me')return route.fulfill({json:{username:'admin',role:'admin'}});
  if(path==='/settings')return route.fulfill({json:{configured:true,has_token:true,settings:{public_url:'https://panel.example.com',agent_url:'https://panel.example.com:9443'}}});
  if(path==='/groups')return route.fulfill({json:[{id:'default',name:'پیش‌فرض'}]});
  if(path==='/nodes'&&route.request().method()==='POST'){
   const body=route.request().postDataJSON();expect(body.shared_host).toBe(true);expect(body.ports).toEqual({reality:12087,websocket:8443,trojan:2053,stats:10086});
   return route.fulfill({status:201,json:{...body,id:'shared-node',domain:'n.example.com',enabled:true,ready:false,verified:[]}});
  }
  if(path==='/ssh/fingerprint'){fingerprintCalls++;return route.fulfill({json:{fingerprint:'SHA256:test-fingerprint'}})}
  return route.fulfill({json:[]});
 });
 await page.goto('/');
 await page.getByRole('button',{name:'افزودن سرور',exact:true}).click();
 let dialog=page.getByRole('dialog');
 await dialog.getByLabel('نام سرور',{exact:true}).fill('سرور پنل');
 await dialog.getByLabel('آدرس IP',{exact:true}).fill('203.0.113.10');
 await dialog.getByLabel('این نود روی سرور پنل یا کنار سرویس‌های دیگر نصب می‌شود.').check();
 await dialog.locator('summary').click();
 await expect(dialog.getByLabel('پورت REALITY',{exact:true})).toHaveValue('2087');
 await expect(dialog.getByLabel('پورت آمار داخلی',{exact:true})).toHaveValue('10086');
 await dialog.getByLabel('پورت REALITY',{exact:true}).fill('12087');
 await dialog.getByRole('button',{name:'ادامه و بررسی دسترسی'}).click();
 dialog=page.getByRole('dialog');
 await expect(dialog.getByRole('heading',{name:'راه‌اندازی سرور پنل'})).toBeVisible();
 await dialog.locator('summary').click();
 await expect(dialog.getByLabel('پورت REALITY',{exact:true})).toHaveValue('12087');
 await dialog.getByLabel('پورت REALITY',{exact:true}).fill('443');
 await dialog.getByRole('button',{name:'دریافت اثر انگشت SSH'}).click();
 await expect(dialog.getByRole('alert')).toContainText('برای پنل محفوظ‌اند');expect(fingerprintCalls).toBe(0);
 await dialog.getByLabel('پورت REALITY',{exact:true}).fill('22087');
 await dialog.getByRole('button',{name:'دریافت اثر انگشت SSH'}).click();
 await expect(dialog.getByLabel('نام کاربری SSH (root یا sudo بدون تعامل)')).toBeVisible();expect(fingerprintCalls).toBe(1);
});
