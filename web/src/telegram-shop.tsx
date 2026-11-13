import {useEffect,useState,type FormEvent} from 'react';
import {api} from './api';
import {bytes} from './helpers';
import type {Group} from './types';

type Plan = {
 id:string;
 name:string;
 days:number;
 quota_bytes:number;
 group_id:string;
 stars:number;
 purchase_limit:number;
 enabled:boolean;
};
type Order = {
 id:string;
 telegram_id:number;
 user_id:string;
 user_name:string;
 plan:Plan;
 status:string;
 charge_id:string;
 created_at:string;
};

const orderStatus:Record<string,string> = {
 pending:'در انتظار تأیید', invoiced:'فاکتور صادر شد', approved:'در انتظار نتیجه پرداخت',
 fulfilled:'انجام شد', review:'نیاز به پیگیری',
};

export function TelegramShop({groups,canEdit}:{groups:Group[];canEdit:boolean}) {
 const [plans,setPlans]=useState<Plan[]>([]);
 const [orders,setOrders]=useState<Order[]>([]);
 const [more,setMore]=useState(false);
 const [editing,setEditing]=useState<Plan|null>(null);
 const [error,setError]=useState('');
 const [busy,setBusy]=useState(false);

 async function load() {
  const [p,o]=await Promise.all([api<Plan[]>('/bot/plans'),api<Order[]>('/bot/orders')]);
  setPlans(p);
  setOrders(o);
  setMore(o.length===100);
 }
 useEffect(()=>{load().catch(e=>setError(e.message))},[]);

 async function save(event:FormEvent<HTMLFormElement>) {
  event.preventDefault();
  if(!editing)return;
  const form=new FormData(event.currentTarget);
  const quota=Math.round(Number(form.get('quota'))*2**30);
  if(!Number.isSafeInteger(quota)){setError('حجم خارج از محدوده است.');return}
  setBusy(true);
  setError('');
  try {
   await api(editing.id?'/bot/plans/'+editing.id:'/bot/plans',editing.id?'PUT':'POST',{
    name:form.get('name'),days:Number(form.get('days')),quota_bytes:quota,
    group_id:form.get('group'),stars:Number(form.get('stars')),enabled:form.has('enabled'),
    purchase_limit:Number(form.get('purchase_limit')),
   });
   setEditing(null);
   await load();
  } catch(e){setError((e as Error).message)}
  finally{setBusy(false)}
 }
 async function older() {
  setBusy(true);
  setError('');
  try {
   const page=await api<Order[]>('/bot/orders?before='+encodeURIComponent(orders[orders.length-1].created_at));
   setOrders([...orders,...page]);
   setMore(page.length===100);
  } catch(e){setError((e as Error).message)}
  finally{setBusy(false)}
 }

 return <>
  {canEdit&&<TelegramSettings/>}
  {error&&<p role="alert" className="form-error">{error}</p>}
  <div className="section-heading"><h2>پلن‌های بات</h2>{canEdit&&<button className="primary" onClick={()=>setEditing({id:'',name:'',days:30,quota_bytes:0,group_id:groups[0]?.id??'',stars:0,purchase_limit:0,enabled:true})}>پلن جدید</button>}</div>
  {editing&&<form className="card settings-form" key={editing.id} onSubmit={save}>
   <label className="field">نام<input name="name" required maxLength={100} defaultValue={editing.name}/></label>
   <label className="field">مدت (روز)<input type="number" name="days" min={1} max={3650} required defaultValue={editing.days}/></label>
   <label className="field">حجم (GiB، صفر یعنی نامحدود)<input type="number" name="quota" min={0} step="any" required defaultValue={editing.quota_bytes/2**30}/></label>
   <label className="field">گروه VPN<select name="group" required defaultValue={editing.group_id}>{groups.map(g=><option key={g.id} value={g.id}>{g.name}</option>)}</select></label>
   <label className="field">قیمت (Stars، صفر یعنی رایگان)<input type="number" name="stars" min={0} max={2147483647} required defaultValue={editing.stars}/></label>
   <label><input type="checkbox" name="enabled" defaultChecked={editing.enabled}/> فعال</label>
   <label className="field">محدودیت خرید برای هر کاربر<select name="purchase_limit" defaultValue={editing.purchase_limit??0}>
    <option value={0}>بدون محدودیت</option>
    <option value={1}>فقط یک‌بار</option>
    <option value={2}>یک‌بار در هر ۳۰ روز</option>
   </select></label>
   <div className="actions"><button className="primary" disabled={busy}>ذخیره پلن</button><button className="secondary" type="button" onClick={()=>setEditing(null)}>انصراف</button></div>
  </form>}
  <section className="card table-wrap"><table><thead><tr><th>پلن</th><th>مدت</th><th>حجم</th><th>گروه</th><th>Stars</th><th>وضعیت</th>{canEdit&&<th>عملیات</th>}</tr></thead>
   <tbody>{plans.map(p=><tr key={p.id}><td>{p.name}</td><td>{p.days} روز</td><td>{p.quota_bytes?bytes(p.quota_bytes):'نامحدود'}</td><td>{groups.find(g=>g.id===p.group_id)?.name}</td><td>{p.stars||'رایگان'}</td><td>{p.enabled?'فعال':'غیرفعال'}</td>{canEdit&&<td><button className="text-btn" onClick={()=>setEditing(p)}>ویرایش</button></td>}</tr>)}</tbody>
  </table>{!plans.length&&<p>پلنی تعریف نشده است.</p>}</section>
  <div className="section-heading"><h2>سفارش‌ها</h2><button className="secondary" onClick={()=>load().catch(e=>setError(e.message))}>به‌روزرسانی سفارش‌ها</button></div>
  <section className="card table-wrap"><table><thead><tr><th>کاربر</th><th>پلن</th><th>Stars</th><th>وضعیت</th><th>شناسه پرداخت</th><th>زمان</th></tr></thead>
   <tbody>{orders.map(o=><tr key={o.id}><td>{o.user_name||o.user_id}<small dir="ltr">{o.telegram_id}</small></td><td>{o.plan.name}</td><td>{o.plan.stars||'رایگان'}</td><td>{orderStatus[o.status]??o.status}</td><td><bdi>{o.charge_id||'—'}</bdi></td><td>{new Date(o.created_at).toLocaleString('fa-IR')}</td></tr>)}</tbody>
  </table>{!orders.length&&<p>سفارشی ثبت نشده است.</p>}{more&&<button className="secondary" disabled={busy} onClick={older}>سفارش‌های قدیمی‌تر</button>}</section>
 </>;
}

type BotSettings = {has_token:boolean;terms:string;support:string};

function TelegramSettings() {
 const [settings,setSettings]=useState<BotSettings|null>(null);
 const [token,setToken]=useState('');
 const [busy,setBusy]=useState(false);
 const [notice,setNotice]=useState('');
 const [error,setError]=useState('');

 useEffect(()=>{
  api<BotSettings>('/bot/settings').then(setSettings).catch(e=>setError(e.message));
 },[]);

 async function save(removeToken=false) {
  if(!settings)return;
  setBusy(true);
  setError('');
  setNotice('');
  try {
   setSettings(await api<BotSettings>('/bot/settings','PUT',{
    token,terms:settings.terms,support:settings.support,remove_token:removeToken,
   }));
   setToken('');
   setNotice(removeToken?'توکن حذف شد و بات متوقف می‌شود.':'تنظیمات ذخیره شد.');
  } catch(e){setError((e as Error).message)}
  finally{setBusy(false)}
 }

 return <section>
  <h2>تنظیمات بات</h2>
  {error&&<p role="alert" className="form-error">{error}</p>}
  {notice&&<p role="status">{notice}</p>}
  {settings&&<form className="card settings-form" onSubmit={e=>{e.preventDefault();void save()}}>
   <p>{settings.has_token?'توکن ثبت شده است. برای حفظ توکن فعلی، فیلد را خالی بگذارید.':'توکن ثبت نشده است.'}</p>
   <label className="field">توکن BotFather<input type="password" dir="ltr" autoComplete="new-password" value={token} onChange={e=>setToken(e.target.value)}/></label>
   <label className="field">شرایط خرید<textarea maxLength={3500} value={settings.terms} onChange={e=>setSettings({...settings,terms:e.target.value})}/></label>
   <label className="field">راه تماس پشتیبانی<input maxLength={3500} value={settings.support} onChange={e=>setSettings({...settings,support:e.target.value})}/></label>
   <div className="actions">
    <button className="primary" disabled={busy}>ذخیره تنظیمات بات</button>
    {settings.has_token&&<button type="button" className="secondary danger" disabled={busy} onClick={()=>{if(confirm('توکن حذف و بات متوقف شود؟'))void save(true)}}>حذف توکن و توقف بات</button>}
   </div>
  </form>}
 </section>;
}

export function TelegramLink({userID}:{userID:string}) {
 const path='/users/'+userID+'/telegram';
 const [linked,setLinked]=useState<string|null>(null);
 const [code,setCode]=useState('');
 const [error,setError]=useState('');
 const [busy,setBusy]=useState(false);
 useEffect(()=>{
  api<{telegram_id:string}>(path).then(v=>setLinked(v.telegram_id)).catch(e=>setError(e.message));
 },[path]);

 async function update(method:'POST'|'DELETE') {
  setBusy(true);
  setError('');
  try {
   if(method==='POST'){
    const result=await api<{code:string}>(path,'POST');
    setCode(result.code);
   }else{
    await api(path,'DELETE');
    setLinked('');
    setCode('');
   }
  } catch(e){setError((e as Error).message)}
  finally{setBusy(false)}
 }

 return <section>
  <h3>اتصال تلگرام</h3>
  {linked?<p>شناسه تلگرام <bdi>{linked}</bdi></p>:linked===''&&<p>متصل نشده است.</p>}
  {linked!==null&&<div className="actions">
   {!linked&&<button className="secondary" disabled={busy} onClick={()=>update('POST')}>صدور کد اتصال</button>}
   <button className="secondary" disabled={busy} onClick={()=>{if(confirm('اتصال و کد فعلی تلگرام لغو شود؟'))update('DELETE')}}>{linked?'قطع اتصال':'لغو کد اتصال'}</button>
  </div>}
  {code&&<label className="field">این دستور را تا ۱۵ دقیقه در بات وارد کنید.<input dir="ltr" readOnly value={'/link '+code} onFocus={e=>e.currentTarget.select()}/></label>}
  {error&&<p role="alert" className="form-error">{error}</p>}
 </section>;
}
