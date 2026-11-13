import {useEffect,useState} from 'react';
import {api} from './api';

export function BulkControls({ids, done}:{ids:string[];done:()=>Promise<unknown>}) {
 const [action,setAction]=useState('extend');
 const [amount,setAmount]=useState(1);
 const [pending,setPending]=useState<{key:string;action:string;ids:string[];amount:number;confirmed:number}>();
 const [busy,setBusy]=useState(false),[error,setError]=useState('');
 const titles:Record<string,string>={extend:'تمدید (روز)',quota:'افزایش سهمیه (گیگابایت)',enable:'فعال‌سازی',suspend:'تعلیق',reset:'ریست مصرف',delete:'حذف'};
 if(!ids.length&&!pending)return null;
 async function submit(){
  const request=pending ?? {key:crypto.randomUUID(),action,ids,amount:action==='quota'?amount*2**30:action==='extend'?amount:0,confirmed:ids.length};
  if(!pending&&!confirm(titles[action]+' برای '+ids.length+' کاربر انجام شود؟'))return;
  setPending(request);setBusy(true);setError('');
  try {await api('/users/bulk','POST',request);setPending(undefined);await done()}
  catch(e){setError((e as Error).message)}
  finally{setBusy(false)}
 }
 return <div className="actions">
  <label>عملیات گروهی <select value={action} disabled={!!pending} onChange={e=>setAction(e.target.value)}>{Object.entries(titles).map(([key,title])=><option key={key} value={key}>{title}</option>)}</select></label>
  {(action==='extend'||action==='quota')&&<input aria-label="مقدار تغییر" type="number" min={1} step={1} value={amount} disabled={!!pending} onChange={e=>setAmount(e.target.valueAsNumber)}/>}
  <button className="secondary" disabled={busy||(!pending&&(ids.length>100||!Number.isSafeInteger(amount)||amount<1))} onClick={submit}>{pending?'تلاش مجدد':'اعمال برای '+ids.length+' کاربر'}</button>
  {pending&&!busy&&<button className="text-btn" onClick={()=>{setPending(undefined);setError('')}}>بستن درخواست</button>}
  {error&&<p role="alert">{error}</p>}
 </div>
}

export function AccountLink({userID,onRevoked}:{userID:string;onRevoked:()=>void}) {
 const [link,setLink]=useState(''),[message,setMessage]=useState(''),[busy,setBusy]=useState(false);
 async function change(action:string){
  if(!confirm(action==='issue'?'لینک صفحه صادر شود؟ لینک قبلی صفحه باطل می‌شود.':'لینک انتخاب‌شده باطل شود؟'))return;
  setBusy(true);setMessage('');
  try {const data=await api<{link:string}>('/users/'+userID+'/account','POST',{action});setLink(data.link?location.origin+data.link:'');setMessage(data.link?'لینک را اکنون کپی کنید.':'ابطال انجام شد.');if(action==='revoke-both')onRevoked()}
  catch(e){setMessage((e as Error).message)}
  finally{setBusy(false)}
 }
 return <section>
  <h3>صفحهٔ اشتراک کاربر</h3>
  <p>دارندهٔ لینک صفحه می‌تواند لینک اتصال را بردارد. قطع دسترسی کانفیگ‌های دریافت‌شده به چرخش اعتبارنامهٔ اتصال نیاز دارد.</p>
  <div className="actions">
   <button className="secondary" disabled={busy} onClick={()=>change('issue')}>صدور لینک صفحه</button>
   <button className="text-btn" disabled={busy} onClick={()=>change('revoke')}>ابطال لینک صفحه</button>
   <button className="text-btn danger" disabled={busy} onClick={()=>change('revoke-both')}>ابطال هر دو لینک</button>
  </div>
  {link&&<input aria-label="لینک محرمانهٔ صفحه" dir="ltr" value={link} readOnly onFocus={e=>e.currentTarget.select()}/>}
  {message&&<p role="status">{message}</p>}
 </section>
}

export function UserActivity({userID}:{userID:string}) {
 const [rows,setRows]=useState<{node:string;sampled_at:string|null}[]>();
 const [error,setError]=useState('');
 useEffect(()=>{
  let cancelled=false;
  api<{node:string;sampled_at:string|null}[]>('/users/'+userID+'/activity')
   .then(result=>{if(!cancelled)setRows(result)})
   .catch(e=>{if(!cancelled)setError(e.message)});
  return()=>{cancelled=true};
 },[userID]);
 return <section>
  <h3>آخرین ترافیک ثبت‌شده</h3>
  {error?<p role="alert">{error}</p>:!rows?<p>در حال بارگذاری…</p>:rows.length===0?<p>نامشخص</p>:
   <dl>{rows.map(row=><div key={row.node}><dt>{row.node}</dt><dd>{row.sampled_at?new Date(row.sampled_at).toLocaleString('fa-IR'):'نامشخص'}</dd></div>)}</dl>}
 </section>
}

export function ResetPolicy({userID}:{userID:string}) {
 const path='/users/'+userID+'/reset-policy';
 type Policy={days:number;next:string|null;ready:boolean};
 const [policy,setPolicy]=useState<Policy>(),[days,setDays]=useState(0),[error,setError]=useState(''),[busy,setBusy]=useState(false);
 useEffect(()=>{
  let cancelled=false;
  api<Policy>(path).then(data=>{if(!cancelled){setPolicy(data);setDays(data.days)}}).catch(e=>{if(!cancelled)setError(e.message)});
  return()=>{cancelled=true};
 },[path]);
 async function save(){
  setBusy(true);setError('');
  try{setPolicy(await api<Policy>(path,'PUT',{days}))}
  catch(e){setError((e as Error).message)}
  finally{setBusy(false)}
 }
 return <section><h3>ریست دوره‌ای سهمیه</h3>
  <p>صفر یعنی غیرفعال. ترافیک یک نمونه به دورهٔ زمان پایان آن تعلق می‌گیرد.</p>
  {policy&&<><label>دوره به روز <input type="number" min={0} max={3650} step={1} value={days} onChange={e=>setDays(e.target.valueAsNumber)}/></label>
   <button className="secondary" disabled={busy||!Number.isInteger(days)||days<0||days>3650||(!policy.ready&&days!==0)} onClick={save}>ذخیرهٔ دوره</button>
   {policy.next&&<p>موعد بعدی {new Date(policy.next).toLocaleString('fa-IR')}</p>}
   {!policy.ready&&<p role="status">ریست خودکار منتظر گزارش زمان‌دار و تازه از همهٔ نودهای فعال گروه است.</p>}
  </>}
  {error&&<p role="alert">{error}</p>}
 </section>
}

export function BulkCreate({groups,done}:{groups:{id:string;name:string}[];done:()=>Promise<unknown>}) {
 const [pending,setPending]=useState<{key:string;action:string;names:string[];group_id:string;days:number;quota_bytes:number}>();
 const [busy,setBusy]=useState(false),[error,setError]=useState('');
 return <details><summary>ساخت گروهی کاربران</summary>
  <form onSubmit={async e=>{
   e.preventDefault();
   const form=e.currentTarget,fields=new FormData(form);
   const names=String(fields.get('names')).split(/\r?\n/).map(name=>name.trim()).filter(Boolean);
   const request=pending??{key:crypto.randomUUID(),action:'create',names,group_id:String(fields.get('group')),days:Number(fields.get('days')),quota_bytes:Number(fields.get('quota'))*2**30};
   if(request.names.length<1||request.names.length>100||!Number.isSafeInteger(request.quota_bytes)){setError('بین ۱ تا ۱۰۰ نام و سهمیهٔ معتبر وارد کنید.');return}
   if(!pending&&!confirm('ساخت '+request.names.length+' کاربر با تنظیمات واردشده انجام شود؟'))return;
   setPending(request);setBusy(true);setError('');
   try{await api('/users/bulk','POST',request);setPending(undefined);form.reset();await done()}
   catch(e){setError((e as Error).message)}
   finally{setBusy(false)}
  }}>
   <fieldset disabled={busy||!!pending}>
    <label>نام‌ها، هر نام در یک خط <textarea name="names" required/></label>
    <label>گروه <select name="group" required>{groups.map(group=><option key={group.id} value={group.id}>{group.name}</option>)}</select></label>
    <label>اعتبار به روز <input name="days" type="number" min={1} max={36500} defaultValue={30} required/></label>
    <label>سهمیه به گیگابایت، صفر یعنی نامحدود <input name="quota" type="number" min={0} defaultValue={0} required/></label>
   </fieldset>
   <button className="secondary" disabled={busy}>{pending?'تلاش مجدد':'ساخت گروهی'}</button>
   {pending&&!busy&&<button type="button" onClick={()=>setPending(undefined)}>بستن درخواست</button>}
   {error&&<p role="alert">{error}</p>}
  </form>
 </details>
}
