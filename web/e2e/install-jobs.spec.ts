import {test,expect} from '@playwright/test';
import type {Job} from '../src/types';

for(const width of [390,1440]){
 test(`installation steps track progress and keyboard disclosure at ${width}px`,async({page})=>{
  await page.setViewportSize({width,height:900});
  await page.clock.install();
  const node={id:'test-node',name:'سرور آزمایشی',host:'203.0.113.10',group_id:'default',enabled:true,verified:[]};
  const previous:Job={id:'previous',node_id:'old-node',status:'failed',step:'ssh',error:'Previous failure',updated_at:'2026-11-10T12:00:00Z'};
  let job:Job|undefined;
  await page.route('**/api/v1/**',async route=>{
   const path=new URL(route.request().url()).pathname.replace('/api/v1','');
   if(path==='/me')return route.fulfill({json:{username:'admin',role:'admin'}});
   if(path==='/settings')return route.fulfill({json:{configured:true,settings:{}}});
   if(path==='/nodes')return route.fulfill({json:[node]});
   if(path==='/ssh/fingerprint')return route.fulfill({json:{fingerprint:'SHA256:test'}});
   if(path==='/nodes/test-node/install'){
    expect(route.request().method()).toBe('POST');
    job={id:'new',node_id:node.id,status:'queued',step:'queued',error:'',updated_at:'2026-11-11T12:00:00Z'};
    return route.fulfill({status:202,json:job});
   }
   if(path==='/jobs')return route.fulfill({json:job?[job,previous]:[previous]});
   return route.fulfill({json:[]});
  });

  await page.goto('/');
  await page.getByRole('navigation').getByRole('button',{name:/^سرورها/}).click();
  await page.getByRole('button',{name:'راه‌اندازی',exact:true}).click();
  const form=page.getByRole('dialog');
  await form.getByRole('button',{name:'دریافت اثر انگشت SSH'}).click();
  await form.getByLabel('رمز SSH').fill('test-password');
  await form.getByRole('checkbox',{name:'اثر انگشت سرور را تأیید می‌کنم.'}).check();
  await form.getByRole('button',{name:'تأیید و نصب خودکار'}).click();

  await expect(page.getByRole('heading',{name:'آخرین عملیات'})).toBeVisible();
  const operation=page.getByRole('article').filter({hasText:node.name});
  const toggle=operation.getByRole('button');
  const oldToggle=page.getByRole('button',{name:/old-node/});
  const current=operation.locator('[aria-current="step"]');
  await expect(toggle).toHaveAttribute('aria-expanded','true');
  await expect(operation.getByText('در انتظار',{exact:true})).toHaveCount(5);
  await expect(oldToggle).toHaveAttribute('aria-expanded','true');
  await expect(toggle).toHaveCSS('direction','rtl');

  job={...job!,status:'running',step:'ssh'};
  await page.clock.fastForward(10_000);
  await expect(current).toContainText('آماده‌سازی و اتصال SSH');
  await expect(current).toContainText('در حال اجرا');
  await toggle.focus();
  await page.keyboard.press('Space');
  await expect(toggle).toHaveAttribute('aria-expanded','false');

  job={...job,status:'failed',step:'certificate',error:'Certificate issuance failed'};
  await page.clock.fastForward(10_000);
  await expect(toggle).toContainText('ناموفق');
  await expect(toggle).toHaveAttribute('aria-expanded','false');
  await expect(oldToggle).toHaveAttribute('aria-expanded','false');
  await page.keyboard.press('Enter');
  await expect(current).toContainText('صدور گواهی TLS');
  await expect(current).toContainText('Certificate issuance failed');
  await expect(current).toContainText('مجوزهای Cloudflare');
  await expect(operation.getByText('انجام‌شده',{exact:true})).toHaveCount(2);
 });
}
