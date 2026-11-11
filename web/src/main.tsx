import React,{useEffect,useRef,useState,type FormEvent,type ReactNode} from 'react';
import {createRoot} from 'react-dom/client';
import {QRCodeSVG} from 'qrcode.react';
import {Activity,ArrowUpLeft,Check,CheckCircle2,ChevronLeft,Copy,Globe2,LayoutDashboard,LogOut,Network,Plus,RefreshCw,Server,Settings as SettingsIcon,ShieldCheck,Users,X,Clock,AlertCircle} from 'lucide-react';
import {api} from './api';
import {bytes,online,userPayload,persianDate,expiryFromPersianDate,expiryFromDays,expiryTimeZone} from './helpers';
import {checkedPorts,defaultPorts,PortFields} from './node-ports';
import type {Audit,Group,Job,Node as VPNNode,Settings,User} from './types';
import './style.css';

const userFilters=[
 ['all','همه'],
 ['expiring','نزدیک انقضا'],
 ['nearQuota','نزدیک سقف مصرف'],
 ['expired','منقضی‌شده'],
] as const;
type UserFilter=typeof userFilters[number][0];
type UserSort='usage'|'expiry';
const expiryChartDays=30;

type Tab='overview'|'nodes'|'users'|'jobs'|'settings'|'audit';
const menu=[['overview','نمای کلی',LayoutDashboard],['nodes','سرورها',Server],['users','کاربران',Users],['jobs','عملیات نصب',Activity],['settings','تنظیمات',SettingsIcon],['audit','رویدادها',ShieldCheck]] as const;
const fa=(n:number)=>n.toLocaleString('fa-IR');
const initialSettings:Settings={domain:'',zone_id:'',cloudflare_token:'',email:'',agent_url:'',public_url:location.origin,reality_target:'www.microsoft.com:443',reality_sni:'www.microsoft.com',acme_url:''};
type SettingsResponse={settings:Settings;has_token:boolean;configured:boolean};
const cloudflareFields: ReadonlyArray<keyof Settings> = ['domain','zone_id','cloudflare_token'];
function Field({label,children}: {label:string;children:ReactNode}){return <label className="field"><span>{label}</span>{children}</label>}

function PasswordForm({username}:{username:string}){
 const [busy,setBusy]=useState(false),[error,setError]=useState(''),[saved,setSaved]=useState(false);
 async function submit(e:FormEvent<HTMLFormElement>){
  e.preventDefault();
  const form=e.currentTarget;
  const data=new FormData(form);
  const current=String(data.get('current_password'));
  const password=String(data.get('new_password'));
  setError('');
  setSaved(false);
  if(password!==data.get('confirm_password')){
   setError('رمز جدید و تکرار آن یکسان نیستند.');
   return;
  }
  if([...password].length<6){
   setError('رمز جدید باید حداقل ۶ کاراکتر داشته باشد.');
   return;
  }
  if(new TextEncoder().encode(password).length>72){
   setError('رمز جدید نباید بیشتر از ۷۲ بایت باشد.');
   return;
  }
  setBusy(true);
  try{
   await api('/me/password','PUT',{current_password:current,new_password:password});
   form.reset();
   setSaved(true);
  }catch(e){
   const message=(e as Error).message;
   const translations:Record<string,string>={
    'Current password is incorrect':'رمز فعلی درست نیست.',
    'Too many password change attempts; retry in one minute':'تعداد تلاش‌ها زیاد است. یک دقیقه دیگر دوباره تلاش کنید.',
    'Password or session changed; sign in again and retry':'رمز یا نشست تغییر کرده است. دوباره وارد شوید و تلاش کنید.',
    'Session expired':'نشست منقضی شده است. دوباره وارد شوید.',
   };
   setError(translations[message]||'تغییر رمز انجام نشد. دوباره تلاش کنید.');
  }finally{
   setBusy(false);
  }
 }
 return <form className="card settings-form" onSubmit={submit}>
  <h2>تغییر رمز ورود ادمین</h2>
  <p className="muted" id="password-hint">رمز جدید باید حداقل ۶ کاراکتر داشته باشد. پس از تغییر رمز، نشست‌های دیگر این حساب بسته می‌شوند.</p>
  <input hidden name="username" autoComplete="username" value={username} readOnly/>
  <div className="form-grid">
   <Field label="رمز فعلی"><input name="current_password" dir="ltr" type="password" autoComplete="current-password" disabled={busy} required/></Field>
   <Field label="رمز جدید"><input name="new_password" dir="ltr" type="password" autoComplete="new-password" aria-describedby="password-hint" disabled={busy} required/></Field>
   <Field label="تکرار رمز جدید"><input name="confirm_password" dir="ltr" type="password" autoComplete="new-password" disabled={busy} required/></Field>
  </div>
  {error&&<p role="alert" className="form-error">{error}</p>}
  {saved&&<p role="status" className="banner success">رمز ورود تغییر کرد.</p>}
  <footer><button className="primary" disabled={busy}>{busy?'در حال تغییر…':'ذخیره رمز جدید'}</button></footer>
 </form>;
}
function Modal({title,close,children}:{title:string;close:()=>void;children:ReactNode}){useEffect(()=>{const fn=(e:KeyboardEvent)=>{if(e.key==='Escape')close()};document.addEventListener('keydown',fn);const previous=document.activeElement as HTMLElement|null;const first=document.querySelector<HTMLElement>('[role="dialog"] input,[role="dialog"] button');first?.focus();return()=>{document.removeEventListener('keydown',fn);previous?.focus()}},[close]);return <div className="overlay" onClick={e=>{if(e.target===e.currentTarget)close()}}><section role="dialog" aria-modal="true" aria-label={title} className="modal" onKeyDown={e=>{if(e.key!=='Tab')return;const els=Array.from(e.currentTarget.querySelectorAll<HTMLElement>('button:not(:disabled),input,select,textarea,a[href]'));const first=els[0],last=els[els.length-1];if(e.shiftKey&&document.activeElement===first){e.preventDefault();last?.focus()}else if(!e.shiftKey&&document.activeElement===last){e.preventDefault();first?.focus()}}}><header><h2>{title}</h2><button className="icon" aria-label="بستن" onClick={close}><X size={20}/></button></header>{children}</section></div>}
function App(){
 const [account,setAccount]=useState<{username:string;role:string}|null>(null),[checking,setChecking]=useState(true),[tab,setTab]=useState<Tab>('overview');
 const [users,setUsers]=useState<User[]>([]),[nodes,setNodes]=useState<VPNNode[]>([]),[groups,setGroups]=useState<Group[]>([]),[jobs,setJobs]=useState<Job[]>([]),[audit,setAudit]=useState<Audit[]>([]);
 const [settings,setSettings]=useState<Settings>(initialSettings),[hasToken,setHasToken]=useState(false),[configured,setConfigured]=useState(false),[error,setError]=useState(''),[notice,setNotice]=useState(''),[busy,setBusy]=useState(false),[loaded,setLoaded]=useState(false);
 const [userFilter,setUserFilter]=useState<UserFilter>('all');
 const [userSort,setUserSort]=useState<{key:UserSort;descending:boolean}|null>(null);
 const [modal,setModal]=useState<'user'|'node'|'group'|null>(null),[editing,setEditing]=useState<User|null>(null),[subscription,setSubscription]=useState<User|null>(null),[installing,setInstalling]=useState<VPNNode|null>(null),[search,setSearch]=useState('');
 const [renaming,setRenaming]=useState<VPNNode|null>(null);
 async function load(){const [u,n,g,j]=await Promise.all([api<User[]>('/users'),api<VPNNode[]>('/nodes'),api<Group[]>('/groups'),api<Job[]>('/jobs')]);setUsers(u);setNodes(n);setGroups(g);setJobs(j);setLoaded(true)}
 const [testingCloudflare,setTestingCloudflare]=useState(false);
 const [cloudflareResult,setCloudflareResult]=useState<{ok:boolean;message:string}|null>(null);
 const settingsRevision=useRef(0);
 async function loadSettings(){const v=await api<SettingsResponse>('/settings');settingsRevision.current++;setSettings({...initialSettings,...v.settings,cloudflare_token:''});setCloudflareResult(null);setHasToken(v.has_token);setConfigured(v.configured);return v}
 async function testCloudflare(){
  const {domain,zone_id,cloudflare_token}=settings;
  const revision=settingsRevision.current;
  setCloudflareResult(null);
  setTestingCloudflare(true);
  try {
   await api('/settings/cloudflare/test','POST',{domain,zone_id,cloudflare_token});
   if(revision===settingsRevision.current){
    setCloudflareResult({ok:true,message:'تست Cloudflare موفق بود. رکورد TXT ساخته و حذف شد.'});
   }
  } catch(e) {
   if(revision===settingsRevision.current){
    const message=e instanceof Error&&!(e instanceof TypeError||e instanceof SyntaxError)
     ?e.message:'ارتباط با پنل برقرار نشد';
    setCloudflareResult({ok:false,message});
   }
  } finally {
   setTestingCloudflare(false);
  }
 }
 useEffect(()=>{api<{username:string;role:string}>('/me').then(setAccount).catch(()=>{}).finally(()=>setChecking(false))},[]);
 useEffect(()=>{if(!account)return;load().catch(e=>setError(e.message));const timer=setInterval(()=>load().catch(()=>{}),10000);return()=>clearInterval(timer)},[account]);
 useEffect(()=>{if(!account)return;loadSettings().then(v=>{if(!v.configured&&account.role==='admin'){setTab('settings');setNotice('به راه خوش آمدید؛ تنظیمات Cloudflare را وارد کنید، سپس اولین سرور را اضافه کنید.')}}).catch(e=>setError(e.message))},[account]);
 useEffect(()=>{if(!account)return;if(tab==='settings')loadSettings().catch(e=>setError(e.message));if(tab==='audit')api<Audit[]>('/audit').then(setAudit).catch(e=>setError(e.message))},[tab,account]);
 async function perform(fn:()=>Promise<unknown>,message:string){setError('');setNotice('');setBusy(true);try{await fn();await load();setNotice(message)}catch(e){setError((e as Error).message)}finally{setBusy(false)}}
 if(checking)return <main className="login"><p>در حال اتصال به پنل…</p></main>;
 if(!account)return <Login onLogin={()=>api<{username:string;role:string}>('/me').then(setAccount)}/>;
 const now=Date.now();
 const userRows=tab==='users'?users.map(user=>{
   const remainingDays=Math.max(0,(Date.parse(user.expires_at)-now)/86_400_000);
   const usage=user.quota_bytes>0?user.used_bytes/user.quota_bytes:null;
   const expired=remainingDays===0;
   const status=!user.enabled?'تعلیق‌شده':expired?'منقضی‌شده':usage!==null&&usage>=1?'سهمیه تمام شده':'فعال';
   return {user,remainingDays,usage,status,all:true,expired,
     expiring:!expired&&remainingDays<=3,nearQuota:usage!==null&&usage>=0.8&&usage<1};
 }):[];
 const searchedUserRows=userRows.filter(({user})=>user.name.toLocaleLowerCase().includes(search.trim().toLocaleLowerCase()));
 const visibleUserRows=searchedUserRows.filter(row=>row[userFilter]);
 if(userSort){
   const {key,descending}=userSort;
   const direction=descending?-1:1;
   visibleUserRows.sort((a,b)=>{
     if(key==='expiry')return direction*(a.remainingDays-b.remainingDays);
     if(a.usage===null)return b.usage===null?0:1;
     if(b.usage===null)return -1;
     return direction*(a.usage-b.usage);
   });
 }
 function toggleUserSort(key:UserSort){
   setUserSort(previous=>({key,descending:previous?.key===key?!previous.descending:key==='usage'}));
 }
 function userSortDirection(key:UserSort){
   return userSort?.key===key?(userSort.descending?'descending':'ascending'):undefined;
 }
 async function copySubscription(user:User){
   try{
     await navigator.clipboard.writeText(subscriptionLink(settings.public_url||location.origin,user));
     setError('');
     setNotice('لینک اشتراک کپی شد');
   }catch{
     setNotice('');
     setSubscription(user);
   }
 }
 const live=nodes.filter(n=>online(n)).length,active=users.filter(u=>u.enabled&&Date.parse(u.expires_at)>Date.now()&&(!u.quota_bytes||u.used_bytes<u.quota_bytes)).length;
 const used=users.reduce((n,u)=>n+u.used_bytes,0),canEdit=account.role==='admin';
 return <div className="app"><aside className="sidebar"><a href="#" className="brand" onClick={e=>{e.preventDefault();setTab('overview')}}><span className="brandmark"><Network size={24}/></span><strong>راه<span>مدیریت یکپارچه شبکه</span></strong></a><div className="workspace"><span className="workspace-dot"/>فضای مدیریت<span className="version">نسخه ۰.۱</span></div><nav aria-label="منوی اصلی">{menu.map(([id,label,Icon])=><button key={id} aria-current={tab===id?'page':undefined} className={tab===id?'selected':''} onClick={()=>{setTab(id);setSearch('');setError('')}}><Icon size={19}/>{label}{id==='nodes'&&<small>{fa(nodes.length)}</small>}{id==='users'&&<small>{fa(users.length)}</small>}</button>)}</nav><div className="sidebar-note"><ShieldCheck size={22}/><strong>کنترل در دست شماست</strong><p>یک پنل برای سرورها، اتصال‌ها و کاربران شما.</p></div><div className="profile"><span className="avatar">{account.username.slice(0,1).toUpperCase()}</span><div><strong>{account.username}</strong><small>{canEdit?'مدیر سیستم':'مشاهده‌گر'}</small></div><button className="icon" aria-label="خروج" onClick={()=>api('/logout','POST').then(()=>{setAccount(null);setLoaded(false)})}><LogOut size={18}/></button></div></aside>
 <div className="content"><header className="topbar"><span>فضای مدیریت <ChevronLeft size={14}/> {menu.find(m=>m[0]===tab)?.[1]}</span><span className="top-meta"><span className="dot"/>{loaded?'داده‌های پنل':'در حال دریافت'}<span className="divider"/>{new Date().toLocaleDateString('fa-IR',{day:'numeric',month:'long',year:'numeric'})}</span></header>
 <main><div className="page-heading"><div><div className="eyebrow">زیرساخت شما، یک‌جا</div><h1>{menu.find(m=>m[0]===tab)?.[1]}</h1><p>{tab==='overview'?'وضعیت شبکه را ببینید و مسیر بعدی را مدیریت کنید.':tab==='nodes'?'سرور را معرفی کنید؛ راه‌اندازی را به راه بسپارید.':tab==='users'?'دسترسی‌ها، اشتراک‌ها و مصرف کاربران را مدیریت کنید.':tab==='settings'?'اتصال سرویس‌ها را یک‌بار تنظیم کنید.':'وضعیت و تاریخچه عملیات شبکه'}</p></div><div className="actions"><button className="secondary" disabled={busy} onClick={()=>perform(load,'اطلاعات به‌روز شد')}><RefreshCw size={16}/>به‌روزرسانی</button>{canEdit&&(tab==='overview'||tab==='nodes')&&<button className="primary" onClick={()=>setModal('node')}><Plus size={17}/>افزودن سرور</button>}{canEdit&&tab==='users'&&<button className="primary" onClick={()=>{setEditing(null);setModal('user')}}><Plus size={17}/>کاربر جدید</button>}</div></div>
 {error&&<div role="alert" className="banner error"><AlertCircle size={18}/>{error}<button className="icon" aria-label="بستن خطا" onClick={()=>setError('')}><X size={16}/></button></div>}{notice&&<div role="status" className="banner success"><CheckCircle2 size={18}/>{notice}</div>}
 {tab==='overview'&&<><section className="stats" aria-label="آمار شبکه">{[[Server,'سرورهای در دسترس',fa(live),`از ${fa(nodes.length)} سرور ثبت‌شده`],[Users,'کاربران فعال',fa(active),`از ${fa(users.length)} کاربر`],[Globe2,'مصرف کاربران',bytes(used),'مجموع ارسال و دریافت'],[Activity,'عملیات در حال اجرا',fa(jobs.filter(j=>['queued','running'].includes(j.status)).length),'نصب و راه‌اندازی سرورها']].map(([Icon,label,value,description])=>{const I=Icon as typeof Server;return <article className="stat" key={String(label)}><div className="stat-top"><span>{String(label)}</span><I size={19}/></div><strong>{String(value)}</strong><small>{String(description)}</small></article>})}</section><div className="section-heading"><h2>زیرساخت شبکه <span className="count">{fa(nodes.length)}</span></h2><button className="text-btn" onClick={()=>setTab('nodes')}>همه سرورها <ArrowUpLeft size={16}/></button></div><NodeList nodes={nodes.slice(0,4)} groups={groups} onEdit={canEdit?setRenaming:undefined} onInstall={canEdit?setInstalling:undefined} onToggle={canEdit?n=>perform(()=>api('/nodes/'+n.id,'PUT',{enabled:!n.enabled}),'وضعیت سرور تغییر کرد'):undefined}/><section className="bottom-grid"><article className="card"><div className="section-heading"><h2>مسیر راه‌اندازی</h2><span className="tag">شروع سریع</span></div>{[['تنظیم دامنه و سرویس DNS','settings'],['معرفی سرور و نصب خودکار','nodes'],['ساخت کاربر و دریافت اشتراک','users']].map(([label,destination],i)=><button className="setup-step" key={label} onClick={()=>setTab(destination as Tab)}><span>{fa(i+1)}</span><strong>{label}</strong><ChevronLeft size={17}/></button>)}</article><article className="card connection-note"><div className="note-icon"><Network size={25}/></div><h2>چند مسیر، یک اشتراک</h2><p>REALITY، WebSocket و Trojan در یک لینک اشتراک برای کلاینت‌های آماده؛ فقط اتصال‌های بررسی‌شده منتشر می‌شوند.</p><span className="mono">VLESS / TLS / REALITY</span></article></section></>}
 {tab==='nodes'&&<><div className="toolbar"><input aria-label="جست‌وجوی سرورها" placeholder="جست‌وجوی نام یا IP سرور…" value={search} onChange={e=>setSearch(e.target.value)}/>{canEdit&&<button className="secondary" onClick={()=>setModal('group')}>گروه جدید</button>}</div><NodeList nodes={nodes.filter(n=>(n.name+n.host).includes(search))} groups={groups} onEdit={canEdit?setRenaming:undefined} onInstall={canEdit?setInstalling:undefined} onToggle={canEdit?n=>perform(()=>api('/nodes/'+n.id,'PUT',{enabled:!n.enabled}),'وضعیت سرور تغییر کرد'):undefined}/></>}
 {tab==='users'&&<section className="card users-card">
   <div className="toolbar users-toolbar">
     <input type="search" aria-label="جست‌وجوی کاربران" placeholder="جست‌وجوی کاربر…" value={search} onChange={e=>setSearch(e.target.value)}/>
     <span className="muted" aria-live="polite">{fa(visibleUserRows.length)} از {fa(users.length)} کاربر</span>
   </div>
   <div className="user-filters" role="group" aria-label="فیلتر کاربران">
     {userFilters.map(([key,label])=><button key={key} type="button" aria-pressed={userFilter===key} onClick={()=>setUserFilter(key)}>
       {label} <span>{fa(searchedUserRows.filter(row=>row[key]).length)}</span>
     </button>)}
   </div>
   {visibleUserRows.length>0&&<>
     <div className="users-mobile-sort" role="group" aria-label="مرتب‌سازی کاربران">
       <button className="secondary" onClick={()=>toggleUserSort('usage')}>مصرف سهمیه {userSort?.key==='usage'&&(userSort.descending?'↓':'↑')}</button>
       <button className="secondary" onClick={()=>toggleUserSort('expiry')}>زمان باقی‌مانده {userSort?.key==='expiry'&&(userSort.descending?'↓':'↑')}</button>
     </div>
     <div className="table-wrap users-table-wrap"><table className="users-table" role="table" aria-label="مصرف و اعتبار کاربران">
       <thead role="rowgroup"><tr role="row">
         <th scope="col">کاربر</th><th scope="col">وضعیت</th>
         <th scope="col" aria-sort={userSortDirection('usage')}><button className="user-sort" onClick={()=>toggleUserSort('usage')}>مصرف / سهمیه <span aria-hidden="true">{userSort?.key==='usage'?(userSort.descending?'↓':'↑'):'↕'}</span></button></th>
         <th scope="col" aria-sort={userSortDirection('expiry')}><button className="user-sort" onClick={()=>toggleUserSort('expiry')}>زمان باقی‌مانده <span aria-hidden="true">{userSort?.key==='expiry'?(userSort.descending?'↓':'↑'):'↕'}</span></button><small>مقیاس نمودار: ۳۰ روز</small></th>
         <th scope="col">عملیات</th>
       </tr></thead>
       <tbody role="rowgroup">{visibleUserRows.map(({user:u,remainingDays,usage,status})=>{
         const adminActions=canEdit&&<>
           <button className="text-btn" onClick={()=>{setEditing(u);setModal('user')}}>ویرایش</button>
           <button className="text-btn" onClick={()=>perform(()=>api('/users/'+u.id,'PUT',{...u,enabled:!u.enabled}),'وضعیت کاربر تغییر کرد')}>{u.enabled?'تعلیق':'فعال‌سازی'}</button>
           <button className="text-btn danger" onClick={()=>{if(confirm('کاربر و اعتبارنامه‌های او حذف شوند؟'))perform(()=>api('/users/'+u.id,'DELETE'),'کاربر حذف شد')}}>حذف</button>
         </>;
         return <tr role="row" key={u.id}>
           <th role="rowheader" scope="row" className="user-name"><strong>{u.name}</strong><small>{groups.find(g=>g.id===u.group_id)?.name}</small></th>
           <td role="cell" className="user-status"><span className={'badge '+(status==='فعال'?'good':u.enabled?'user-warning':'neutral')}>{status}</span></td>
           <td role="cell" className="user-metric">
             <span className="user-mobile-label">مصرف / سهمیه</span>
             <bdi dir="ltr">{bytes(u.used_bytes)}{usage!==null&&<> / {bytes(u.quota_bytes)}</>}</bdi>
             {usage!==null ? <>
               <small>{fa(Math.floor(100*usage))}٪ از سهمیه</small>
               <meter className="usage-meter" aria-label="مصرف نسبت به سهمیه" max={u.quota_bytes} value={Math.min(u.used_bytes,u.quota_bytes)}/>
             </> : <small>سهمیه نامحدود</small>}
           </td>
           <td role="cell" className="user-metric">
             <span className="user-mobile-label">زمان باقی‌مانده · مقیاس ۳۰ روز</span>
             <span>{remainingDays===0?'منقضی شده':remainingDays<1?'کمتر از یک روز':fa(Math.floor(remainingDays))+' روز'}</span>
             <small>انقضا: {new Date(u.expires_at).toLocaleDateString('fa-IR-u-ca-persian',{timeZone:expiryTimeZone})}</small>
             <meter className="expiry-meter" aria-label="زمان باقی‌مانده در مقیاس ۳۰ روز" max={expiryChartDays} value={Math.min(remainingDays,expiryChartDays)}/>
           </td>
           <td role="cell" className="user-actions">
             <div className="actions user-desktop-actions"><button className="text-btn" onClick={()=>setSubscription(u)}>اشتراک</button>{adminActions}</div>
             <div className="user-mobile-actions">
               <button className="secondary" onClick={()=>copySubscription(u)}><Copy size={15}/>کپی لینک اشتراک</button>
               <details>
                 <summary aria-label={'عملیات بیشتر برای '+u.name}>بیشتر</summary>
                 <div className="actions" onClick={e=>{if((e.target as HTMLElement).closest('button'))e.currentTarget.closest('details')?.removeAttribute('open')}}>
                   <button className="text-btn" onClick={()=>setSubscription(u)}>جزئیات اشتراک</button>{adminActions}
                 </div>
               </details>
             </div>
           </td>
         </tr>;
       })}</tbody>
     </table></div>
   </>}
   {loaded&&visibleUserRows.length===0&&(users.length===0
     ?<Empty title="هنوز کاربری ندارید" text="پس از نصب اولین سرور، یک کاربر بسازید و اشتراک او را دریافت کنید."/>
     :<Empty title="کاربری پیدا نشد" text="فیلتر یا عبارت جست‌وجو را تغییر دهید."/>)}
 </section>}
 {tab==='jobs'&&<section className="card"><div className="section-heading"><h2>آخرین عملیات</h2><span className="muted">به‌روزرسانی هر ۱۰ ثانیه</span></div>{jobs.length===0?<Empty title="عملیاتی ثبت نشده" text="با نصب اولین نود، مراحل اجرا اینجا نمایش داده می‌شود."/>:jobs.map(j=><div className="job" key={j.id}><span className={'job-icon '+(j.status==='completed'?'good':'')}><Clock size={20}/></span><div><strong>{nodes.find(n=>n.id===j.node_id)?.name||j.node_id}</strong><small><bdi>{j.step}</bdi> · {new Date(j.updated_at).toLocaleString('fa-IR')}</small>{j.error&&<p className="job-error">{j.error}</p>}</div><span className="badge neutral">{({queued:'در صف',running:'در حال اجرا',completed:'تکمیل‌شده',failed:'ناموفق',interrupted:'متوقف‌شده'} as Record<string,string>)[j.status]||j.status}</span></div>)}</section>}
 {tab==='settings'&&canEdit&&<PasswordForm username={account.username}/>}
 {tab==='settings'&&<form className="card settings-form" onSubmit={e=>{
  e.preventDefault();
  if(testingCloudflare)return;
  perform(async()=>{
   await api('/settings','PUT',settings);
   await loadSettings();
   if(!configured)setTab('nodes');
  },'تنظیمات ذخیره شد؛ اکنون می‌توانید سرور اضافه کنید.');
 }}>
  <h2>دامنه و اتصال Cloudflare</h2>
  <p className="muted">{!configured?'مرحله اول راه‌اندازی: دامنه نودها و دسترسی Cloudflare را وارد کنید. ':''}توکن با دسترسی DNS Edit و Zone Read فقط برای همین Zone. توکن ذخیره‌شده دوباره نمایش داده نمی‌شود. آدرس پنل و عامل‌ها هنگام نصب تنظیم شده‌اند.</p>
  <div className="form-grid">{([['domain','دامنه پایه','text'],['zone_id','Cloudflare Zone ID','text'],['cloudflare_token',hasToken?'توکن جدید (خالی = حفظ قبلی)':'Cloudflare API Token','password'],['email','ایمیل دریافت گواهی','email'],['public_url','آدرس HTTPS پنل','url'],['agent_url','آدرس mTLS عامل‌ها، شامل پورت','url'],['reality_target','مقصد REALITY با پورت','text'],['reality_sni','نام سرور REALITY','text'],['acme_url','ACME Directory (خالی = Let’s Encrypt)','url']] as const).map(([key,label,type])=><Field key={key} label={label}><input dir="ltr" type={type} autoComplete={type==='password'?'new-password':'off'} value={settings[key]} readOnly={key==='public_url'||key==='agent_url'} disabled={testingCloudflare&&cloudflareFields.includes(key)} onChange={e=>{
   if(cloudflareFields.includes(key))setCloudflareResult(null);
   setSettings({...settings,[key]:e.target.value});
  }} required={!['cloudflare_token','acme_url'].includes(key)||key==='cloudflare_token'&&!hasToken}/></Field>)}</div>
  {cloudflareResult&&<p role={cloudflareResult.ok?'status':'alert'} className={cloudflareResult.ok?'banner success':'form-error'}>{cloudflareResult.message}</p>}
  <footer className="actions">{canEdit&&<button type="button" className="secondary" disabled={busy||testingCloudflare} onClick={testCloudflare}>{testingCloudflare?'در حال تست…':'تست Cloudflare'}</button>}
  <button className="primary" disabled={busy||testingCloudflare||!canEdit}>{busy?'در حال ذخیره…':configured?'ذخیره تنظیمات':'ذخیره و ادامه به افزودن سرور'}</button></footer></form>}
 {tab==='audit'&&<section className="card table-wrap"><table><thead><tr><th>رویداد</th><th>شناسه هدف</th><th>زمان</th></tr></thead><tbody>{audit.map((a,i)=><tr key={i}><td className="mono">{a.action}</td><td className="mono">{a.target}</td><td>{new Date(a.created_at).toLocaleString('fa-IR')}</td></tr>)}</tbody></table>{audit.length===0&&<Empty title="رویدادی وجود ندارد" text="تغییرات مدیریتی در این بخش ثبت می‌شوند."/>}</section>}
 <footer className="page-footer"><span>راه · مدیریت زیرساخت اتصال</span><span>آمار مصرف با تأخیر همگام‌سازی نمایش داده می‌شود.</span></footer></main></div>
 {modal==='user'&&<UserForm user={editing} groups={groups} close={()=>setModal(null)} save={async value=>{await api('/users'+(editing?'/'+editing.id:''),editing?'PUT':'POST',value);await load();setModal(null);setNotice('کاربر ذخیره شد')}}/>}
 {modal==='node'&&<NodeForm groups={groups} close={()=>setModal(null)} save={async value=>{const n=await api<VPNNode>('/nodes','POST',value);await load();setModal(null);setInstalling(n)}}/>}
 {modal==='group'&&<Modal title="گروه جدید" close={()=>setModal(null)}><form onSubmit={e=>{e.preventDefault();const name=new FormData(e.currentTarget).get('name');perform(()=>api('/groups','POST',{name}),'گروه ساخته شد').then(()=>setModal(null))}}><Field label="نام گروه"><input name="name" required maxLength={100}/></Field><button className="primary">ساخت گروه</button></form></Modal>}
 {renaming&&<EditNodeForm node={renaming} close={()=>setRenaming(null)} save={async (name,host)=>{
  const result=await api<{host_changed:boolean}>('/nodes/'+renaming.id,'PUT',{name,host:host===renaming.host?undefined:host});
  setNodes(current=>current.map(node=>{
   if(node.id!==renaming.id)return node;
   if(result.host_changed)return {...node,name,host,ready:false,verified:[],last_seen:''};
   return {...node,name};
  }));
  setRenaming(null);
  setNotice(result.host_changed?'آدرس IP ذخیره شد. سرور را با آدرس جدید راه‌اندازی کنید.':'اطلاعات سرور ذخیره شد');
 }}/>}
 {installing&&<InstallForm node={installing} close={()=>setInstalling(null)} save={async credentials=>{await api('/nodes/'+installing.id+'/install','POST',credentials);setInstalling(null);setTab('jobs');await load()}}/>}
 {subscription&&<Subscription user={subscription} base={settings.public_url||location.origin} close={()=>setSubscription(null)} rotate={()=>perform(async()=>{await api('/users/'+subscription.id+'/rotate','POST');setSubscription(null)},'لینک قبلی باطل شد؛ اشتراک جدید را باز کنید')}/>}
 </div>
}
function Empty({title,text}:{title:string;text:string}){return <div className="empty"><Network size={30}/><h3>{title}</h3><p>{text}</p></div>}
function NodeList({nodes,groups,onEdit,onInstall,onToggle}:{nodes:VPNNode[];groups:Group[];onEdit?:(n:VPNNode)=>void;onInstall?:(n:VPNNode)=>void;onToggle?:(n:VPNNode)=>void}){return <section className="card node-list">{nodes.length===0?<Empty title="اولین سرور را اضافه کنید" text="IP و دسترسی SSH کافی است؛ نصب و ساخت اتصال‌ها از پنل انجام می‌شود."/>:<div className="table-wrap"><table><thead><tr><th>سرور</th><th>آدرس</th><th>وضعیت</th><th>روش‌های بررسی‌شده</th><th>عملیات</th></tr></thead><tbody>{nodes.map(n=><tr key={n.id}><td><div className="node-name"><span className="server-icon"><Server size={20}/></span><div><strong>{n.name}</strong><small>{groups.find(g=>g.id===n.group_id)?.name}{n.shared_host?' · سرور مشترک':''}</small></div></div></td><td><bdi className="mono">{n.host}</bdi><small className="mono">{n.domain}</small><small className="mono" dir="ltr">R:{n.ports?.reality||443} / WS:{n.ports?.websocket||8443} / T:{n.ports?.trojan||2053}</small></td><td><span className={'badge '+(online(n)?'good':'neutral')}><span className="dot"/>{!n.enabled?'غیرفعال':online(n)?'در دسترس':n.ready?'بدون ارتباط':'آماده نصب'}</span></td><td><div className="tags">{n.verified?.length?n.verified.map(p=><span className="tag mono" key={p} title={n.route_quality?.[p]?.checked_at ? new Date(n.route_quality[p].checked_at).toLocaleString("fa-IR") : undefined}>{p}{n.route_quality?.[p]?.weak ? " · پشتیبان" : n.route_quality?.[p]?.latency_ms ? ` · ${n.route_quality[p].latency_ms} ms` : ""}</span>):<span className="muted">پس از نصب</span>}</div></td><td><div className="actions">{onEdit&&<button className="text-btn" onClick={()=>onEdit(n)}>ویرایش</button>}{onInstall&&<button className="text-btn" onClick={()=>onInstall(n)}>{n.ready?'نصب مجدد':'راه‌اندازی'}<ArrowUpLeft size={14}/></button>}{onToggle&&<button className="text-btn" onClick={()=>onToggle(n)}>{n.enabled?'توقف':'فعال‌سازی'}</button>}</div></td></tr>)}</tbody></table></div>}</section>}
function Login({onLogin}:{onLogin:()=>Promise<unknown>}){const [error,setError]=useState(''),[busy,setBusy]=useState(false);async function submit(e:FormEvent<HTMLFormElement>){e.preventDefault();const f=new FormData(e.currentTarget);setBusy(true);setError('');try{await api('/login','POST',{username:f.get('username'),password:f.get('password')});await onLogin()}catch(e){setError((e as Error).message)}finally{setBusy(false)}}return <main className="login"><div className="login-art"><Network size={72}/><h1>راهِ روشن برای<br/>مدیریت شبکه</h1><p>سرورها و کاربران، در یک فضای یکپارچه.</p><span>RAH / NETWORK CONTROL</span></div><section className="login-box"><span className="brandmark"><Network size={25}/></span><h1>به راه خوش آمدید</h1><p className="muted">برای مدیریت زیرساخت وارد حساب خود شوید.</p><form onSubmit={submit}><Field label="نام کاربری"><input name="username" dir="ltr" autoComplete="username" required/></Field><Field label="رمز عبور"><input name="password" dir="ltr" type="password" autoComplete="current-password" required/></Field>{error&&<p role="alert" className="form-error">{error}</p>}<button className="primary" disabled={busy}>{busy?'در حال ورود…':'ورود به پنل'}<ArrowUpLeft size={18}/></button></form><small className="muted">با نام کاربری و رمز عبور وارد شوید.</small></section></main>}
function UserForm({user,groups,close,save}:{user:User|null;groups:Group[];close:()=>void;save:(v:unknown)=>Promise<void>}){
 const [error,setError]=useState(''),[busy,setBusy]=useState(false);
 const [expiry,setExpiry]=useState(()=>persianDate(new Date(user?.expires_at??expiryFromDays('30')!)));
 const [days,setDays]=useState('');
 const useDays=days.trim()!=='';
 const daysExpiry=expiryFromDays(days);

 return <Modal title={user?'ویرایش کاربر':'کاربر جدید'} close={close}>
  <form onSubmit={async e=>{
   e.preventDefault();
   setBusy(true);
   setError('');
   const f=new FormData(e.currentTarget);
   try{
    const expiresAt=useDays?expiryFromDays(days):expiryFromPersianDate(expiry);
    if(!expiresAt)throw new Error('تعداد روز اعتبار را به‌صورت عدد صحیح مثبت و معتبر وارد کنید.');
    const savedExpiry=!useDays&&user&&persianDate(new Date(expiresAt))===persianDate(new Date(user.expires_at))?user.expires_at:expiresAt;
    const v=userPayload(String(f.get('name')),String(f.get('quota')),savedExpiry,String(f.get('group')));
    await save({...v,enabled:user?.enabled??true});
   }catch(e){setError((e as Error).message)}finally{setBusy(false)}
  }}>
   <Field label="نام کاربر"><input name="name" defaultValue={user?.name} required maxLength={100}/></Field>
   <Field label="حجم به گیگابایت (صفر = نامحدود)"><input name="quota" type="number" min="0" step="0.01" defaultValue={user?user.quota_bytes/1073741824:10} required/></Field>
   <div className="form-grid">
    <Field label="تاریخ انقضای شمسی">
     <input name="expiry" dir="ltr" placeholder="۱۴۰۵/۰۸/۱۹" aria-describedby="expiry-help"
      value={useDays?(daysExpiry?persianDate(new Date(daysExpiry)):''):expiry}
      onChange={e=>setExpiry(e.target.value)} readOnly={useDays} required={!useDays}/>
    </Field>
    <Field label="اعتبار به روز (اختیاری)">
     <input name="days" dir="ltr" inputMode="numeric" placeholder="مثلاً ۳۰" aria-describedby="expiry-help"
      value={days} onChange={e=>setDays(e.target.value)}/>
    </Field>
   </div>
   <p id="expiry-help" className="muted">تاریخ شمسی تا پایان روز به وقت تهران اعتبار دارد. با وارد کردن تعداد روز، اعتبار از زمان ذخیره محاسبه می‌شود.</p>
   <Field label="گروه سرورها"><select name="group" defaultValue={user?.group_id||'default'}>{groups.map(g=><option key={g.id} value={g.id}>{g.name}</option>)}</select></Field>
   {error&&<p role="alert" className="form-error">{error}</p>}
   <button className="primary" disabled={busy}>{busy?'در حال ذخیره…':'ذخیره کاربر'}</button>
  </form>
 </Modal>;
}
function EditNodeForm({node,close,save}:{node:VPNNode;close:()=>void;save:(name:string,host:string)=>Promise<void>}){
 const [busy,setBusy]=useState(false),[error,setError]=useState('');
 async function submit(e:FormEvent<HTMLFormElement>){
  e.preventDefault();
  const data=new FormData(e.currentTarget);
  const name=String(data.get('name')).trim();
  const host=String(data.get('host')).trim();
  if(!name){
   setError('نام سرور را وارد کنید.');
   return;
  }
  setBusy(true);
  setError('');
  try{await save(name,host)}
  catch(e){setError((e as Error).message)}
  finally{setBusy(false)}
 }
 return <Modal title="ویرایش سرور" close={()=>{if(!busy)close()}}><form onSubmit={submit}>
  <Field label="نام سرور"><input name="name" defaultValue={node.name} required maxLength={100}/></Field>
  <Field label="آدرس IP"><input name="host" dir="ltr" defaultValue={node.host} aria-describedby="node-ip-hint" required/></Field>
  <p className="muted" id="node-ip-hint">پس از تغییر IP، سرور را با آدرس جدید راه‌اندازی کنید تا دوباره در اشتراک‌ها نمایش داده شود.</p>
  {error&&<p role="alert" className="form-error">{error}</p>}
  <button className="primary" disabled={busy}>{busy?'در حال ذخیره…':'ذخیره تغییرات'}</button>
 </form></Modal>;
}
function NodeForm({groups,close,save}:{groups:Group[];close:()=>void;save:(v:unknown)=>Promise<void>}){
 const [error,setError]=useState(''),[busy,setBusy]=useState(false),[shared,setShared]=useState(false),[ports,setPorts]=useState(defaultPorts());
 return <Modal title="معرفی سرور جدید" close={close}><p className="muted">Ubuntu 24.04 معرفی کنید؛ نصب روی سرور پنل هم با پورت‌های جدا امکان‌پذیر است.</p><form onSubmit={async e=>{e.preventDefault();const f=new FormData(e.currentTarget);setBusy(true);setError('');try{await save({name:f.get('name'),host:f.get('host'),group_id:f.get('group'),shared_host:shared,ports:checkedPorts(ports,shared)})}catch(e){setError((e as Error).message)}finally{setBusy(false)}}}>
 <Field label="نام سرور"><input name="name" placeholder="مثلاً سرور پنل" required maxLength={100}/></Field>
 <Field label="آدرس IP"><input name="host" dir="ltr" placeholder="203.0.113.10" required/></Field>
 <Field label="گروه"><select name="group">{groups.map(g=><option key={g.id} value={g.id}>{g.name}</option>)}</select></Field>
 <label className="checkbox"><input type="checkbox" checked={shared} onChange={e=>{setShared(e.target.checked);setPorts(defaultPorts(e.target.checked))}}/>این نود روی سرور پنل یا کنار سرویس‌های دیگر نصب می‌شود.</label>
 {shared&&<p className="muted">REALITY روی پورت ۲۰۸۷ نصب می‌شود تا پورت ۴۴۳ پنل آزاد بماند. اشغال‌بودن پورت‌ها پیش از نصب بررسی می‌شود.</p>}
 <PortFields ports={ports} onChange={setPorts}/>
 {error&&<p role="alert" className="form-error">{error}</p>}<button className="primary" disabled={busy}>ادامه و بررسی دسترسی<ChevronLeft size={17}/></button></form></Modal>
}
function InstallForm({node,close,save}:{node:VPNNode;close:()=>void;save:(v:unknown)=>Promise<void>}){
 const [fp,setFP]=useState(''),[port,setPort]=useState(22),[ports,setPorts]=useState(node.ports||defaultPorts(node.shared_host)),[error,setError]=useState(''),[busy,setBusy]=useState(false),[mode,setMode]=useState('password');
 return <Modal title={'راه‌اندازی '+node.name} close={close}><p className="muted">ابتدا اثر انگشت را با کنسول ارائه‌دهنده مقایسه کنید؛ اعتبارنامه فقط برای همین نصب استفاده می‌شود.</p>
 {node.shared_host&&<p className="muted">نصب روی سرور مشترک؛ سرویس‌های دیگر متوقف نمی‌شوند. اگر پورتی اشغال باشد، پیش از نصب خطا نمایش داده می‌شود.</p>}
 <form onSubmit={async e=>{e.preventDefault();setError('');setBusy(true);try{const selected=checkedPorts(ports,node.shared_host);if(!fp){const r=await api<{fingerprint:string}>('/ssh/fingerprint','POST',{host:node.host,port});setFP(r.fingerprint)}else{const f=new FormData(e.currentTarget);await save({host:node.host,port,ports:selected,username:f.get('username'),password:mode==='password'?f.get('password'):'',private_key:mode==='key'?f.get('private_key'):'',fingerprint:fp})}}catch(e){setError((e as Error).message)}finally{setBusy(false)}}}>
 <Field label="پورت SSH"><input type="number" min="1" max="65535" value={port} onChange={e=>{setPort(Number(e.target.value));setFP('')}} required/></Field>
 <PortFields ports={ports} onChange={setPorts} disabled={node.ready||node.ports_locked}/>
 {(node.ready||node.ports_locked)&&<p className="muted">پس از فعال‌سازی هویت نود، پورت‌های آن در نصب مجدد حفظ می‌شوند.</p>}
 {fp&&<><div className="fingerprint"><ShieldCheck size={20}/><code dir="ltr">{fp}</code></div><Field label="نام کاربری SSH (root یا sudo بدون تعامل)"><input name="username" dir="ltr" defaultValue="root" autoComplete="off" required/></Field><Field label="نوع دسترسی"><select value={mode} onChange={e=>setMode(e.target.value)}><option value="password">رمز عبور</option><option value="key">کلید خصوصی</option></select></Field>{mode==='password'?<Field label="رمز SSH"><input name="password" type="password" autoComplete="off" required/></Field>:<Field label="کلید خصوصی بدون گذرواژه"><textarea name="private_key" dir="ltr" rows={5} required/></Field>}<label className="checkbox"><input type="checkbox" required/>اثر انگشت سرور را تأیید می‌کنم.</label></>}
 {error&&<p role="alert" className="form-error">{error}</p>}<button className="primary" disabled={busy}>{busy?'در حال بررسی…':fp?'تأیید و نصب خودکار':'دریافت اثر انگشت SSH'}</button></form></Modal>
}
function subscriptionLink(base:string,user:User){return base.replace(/\/$/,'')+'/sub/'+user.token}
function Subscription({user,base,close,rotate}:{user:User;base:string;close:()=>void;rotate:()=>void}){const link=subscriptionLink(base,user);const [copied,setCopied]=useState(false),[error,setError]=useState('');return <Modal title={'اشتراک '+user.name} close={close}><div className="qr"><QRCodeSVG value={link} size={190} marginSize={3}/></div><p>این لینک را در بخش اشتراک کلاینت وارد و به‌روزرسانی کنید. برای پوشش دستگاه، حالت VPN یا TUN را فعال کنید.</p><div className="copy-row"><input readOnly dir="ltr" aria-label="لینک اشتراک" value={link}/><button className="secondary" aria-label="کپی لینک" onClick={()=>navigator.clipboard.writeText(link).then(()=>setCopied(true)).catch(()=>setError('کپی خودکار ممکن نشد؛ لینک را دستی کپی کنید.'))}>{copied?<Check size={18}/>:<Copy size={18}/>}</button></div>{error&&<p role="alert">{error}</p>}<p className="muted">لینک اشتراک محرمانه است. ابطال لینک، کانفیگ‌های قبلاً دریافت‌شده را باطل نمی‌کند.</p><button className="secondary danger" onClick={()=>{if(confirm('لینک فعلی باطل و لینک جدید صادر شود؟'))rotate()}}>ابطال و صدور لینک جدید</button></Modal>}
createRoot(document.getElementById('root')!).render(<React.StrictMode><App/></React.StrictMode>);
