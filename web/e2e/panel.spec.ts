import {test,expect} from '@playwright/test';

test('Cloudflare test uses the draft without submitting settings',async({page})=>{
 let releaseProbe!:()=>void;
 const probePending=new Promise<void>(resolve=>{releaseProbe=resolve});
 const probes:unknown[]=[];
 let saves=0;
 await page.route('**/api/v1/**',async route=>{
  const request=route.request();
  const path=new URL(request.url()).pathname;
  if(path.endsWith('/me'))return route.fulfill({json:{username:'admin',role:'admin'}});
  if(path.endsWith('/settings')){
   if(request.method()==='PUT')saves++;
   return route.fulfill({json:{settings:{domain:'example.com',zone_id:'saved-zone'},has_token:true,configured:true}});
  }
  if(path.endsWith('/cloudflare/test')){
   expect(request.method()).toBe('POST');
   probes.push(request.postDataJSON());
   if(probes.length===1){
    await probePending;
    return route.fulfill({json:{ok:true}});
   }
   return route.fulfill({status:502,json:{error:'حذف رکورد TXT تأیید نشد'}});
  }
  return route.fulfill({json:[]});
 });
 await page.goto('/');
 await page.getByRole('navigation').getByRole('button',{name:'تنظیمات',exact:true}).click();
 const domain=page.getByLabel('دامنه پایه',{exact:true});
 const zone=page.getByLabel('Cloudflare Zone ID',{exact:true});
 const token=page.getByLabel('توکن جدید (خالی = حفظ قبلی)',{exact:true});
 await expect(zone).toHaveValue('saved-zone');
 await domain.fill('draft.example.com');
 await zone.fill('draft-zone');
 await token.fill('draft-token');
 const testButton=page.getByRole('button',{name:'تست Cloudflare',exact:true});
 const saveButton=page.getByRole('button',{name:'ذخیره تنظیمات',exact:true});
 await testButton.focus();
 await page.keyboard.press('Enter');
 await expect(page.getByRole('button',{name:'در حال تست…',exact:true})).toBeDisabled();
 await expect(saveButton).toBeDisabled();
 releaseProbe();
 await expect(page.getByRole('status')).toContainText('رکورد TXT ساخته و حذف شد');
 await expect(domain).toHaveValue('draft.example.com');
 await expect(zone).toHaveValue('draft-zone');
 await expect(token).toHaveValue('draft-token');
 await token.fill('');
 await expect(page.getByRole('status')).toHaveCount(0);
 await testButton.click();
 await expect(page.getByRole('alert')).toContainText('حذف رکورد TXT تأیید نشد');
 expect(probes).toEqual([
  {domain:'draft.example.com',zone_id:'draft-zone',cloudflare_token:'draft-token'},
  {domain:'draft.example.com',zone_id:'draft-zone',cloudflare_token:''},
 ]);
 expect(saves).toBe(0);
});

test('administrator creates, suspends and removes a user in the Persian panel',async({page})=>{
 test.skip(!process.env.E2E_ADMIN_PASSWORD,'requires a running isolated panel and administrator secrets');
 await page.goto('/');await expect(page.getByRole('heading',{name:'به راه خوش آمدید'})).toBeVisible();await page.screenshot({path:'../.tools/screenshots/login.png',fullPage:true});
 await page.getByLabel('نام کاربری',{exact:true}).fill(process.env.E2E_ADMIN_USERNAME||'admin');await page.getByLabel('رمز عبور',{exact:true}).fill(process.env.E2E_ADMIN_PASSWORD!);await expect(page.locator('input[name="code"]')).toHaveCount(0);await page.getByRole('button',{name:'ورود به پنل'}).click();
 await expect(page.getByRole('heading',{name:'دامنه و اتصال Cloudflare'})).toBeVisible();
 await expect(page.getByLabel('آدرس HTTPS پنل',{exact:true})).toHaveValue(process.env.E2E_BASE_URL||'http://127.0.0.1:8080');
 await expect(page.getByLabel('آدرس HTTPS پنل',{exact:true})).toHaveJSProperty('readOnly',true);
 await expect(page.getByLabel('آدرس mTLS عامل‌ها، شامل پورت',{exact:true})).toHaveValue(/https:\/\/.+:9443/);
 await page.getByRole('navigation').getByRole('button',{name:'نمای کلی',exact:true}).click();await expect(page.getByRole('heading',{name:'نمای کلی',exact:true})).toBeVisible();await page.screenshot({path:'../.tools/screenshots/dashboard.png',fullPage:true});
 await page.getByRole('navigation').getByRole('button',{name:/کاربران/}).click();await page.getByRole('button',{name:'کاربر جدید',exact:true}).click();const dialog=page.getByRole('dialog');const name='smoke-'+Date.now();await dialog.getByLabel('نام کاربر',{exact:true}).fill(name);await dialog.getByRole('button',{name:'ذخیره کاربر'}).click();const row=page.getByRole('row').filter({hasText:name});await expect(row).toBeVisible();await row.getByRole('button',{name:'تعلیق',exact:true}).click();await expect(row.getByRole('button',{name:'فعال‌سازی',exact:true})).toBeVisible();
 await row.getByRole('button',{name:'اشتراک',exact:true}).click();await expect(page.getByRole('dialog').getByLabel('لینک اشتراک')).toHaveValue(/\/sub\//);await page.getByRole('button',{name:'بستن',exact:true}).click();page.once('dialog',d=>d.accept());await row.getByRole('button',{name:'حذف',exact:true}).click();await expect(row).toHaveCount(0);
 await page.getByRole('navigation').getByRole('button',{name:'تنظیمات',exact:true}).click();await expect(page.getByRole('heading',{name:'دامنه و اتصال Cloudflare'})).toBeVisible();await page.screenshot({path:'../.tools/screenshots/settings.png',fullPage:true});
 await page.setViewportSize({width:390,height:844});await page.getByRole('navigation').getByRole('button',{name:/سرورها/}).click();await page.screenshot({path:'../.tools/screenshots/mobile.png',fullPage:true});await page.setViewportSize({width:1440,height:1000});await page.getByRole('button',{name:'خروج',exact:true}).click();await expect(page.getByRole('heading',{name:'به راه خوش آمدید'})).toBeVisible();
});
