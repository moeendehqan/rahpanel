import {useEffect,useState} from 'react';
import {api} from './api';

type Webhooks={url:string;history:{id:string;kind:string;target:string;status:string;occurred_at:string;attempts?:number;delivery:string}[]};
export function WebhookSettings({canEdit}:{canEdit:boolean}) {
 const [data,setData]=useState<Webhooks>(),[error,setError]=useState(''),[busy,setBusy]=useState(false);
 const [destination,setDestination]=useState(''),[secret,setSecret]=useState('');
 async function refresh(){
  const result=await api<Webhooks>('/settings/webhook');setData(result);setDestination(result.url);
 }
 useEffect(()=>{refresh().catch(e=>setError(e.message))},[]);
 return <section className="card settings-form"><h2>وب‌هوک وضعیت</h2>
  {data&&canEdit&&<form onSubmit={async e=>{
   e.preventDefault();
   if(!confirm('ذخیرهٔ تنظیمات، ارسال‌های معلق قبلی را لغو می‌کند. ادامه می‌دهید؟'))return;
   setBusy(true);setError('');
   try{const result=await api<Webhooks>('/settings/webhook','PUT',{url:destination.trim(),secret});setData(result);setSecret('')}
   catch(err){setError((err as Error).message)}
   finally{setBusy(false)}
  }}>
   <label>مقصد HTTPS عمومی، برای غیرفعال کردن خالی بگذارید <input type="url" dir="ltr" value={destination} onChange={e=>setDestination(e.target.value)}/></label>
   <label>کلید امضای وب‌هوک <input type="password" autoComplete="new-password" required={!!destination} minLength={32} value={secret} onChange={e=>setSecret(e.target.value)}/></label>
   <button className="secondary" disabled={busy}>ذخیره</button>
  </form>}
  <button className="text-btn" onClick={()=>api<Webhooks>('/settings/webhook').then(setData).catch(e=>setError(e.message))}>به‌روزرسانی تاریخچه</button>
  {error&&<p role="alert">{error}</p>}
  {!!data?.history.length&&<div className="table-wrap"><table><thead><tr><th>رویداد</th><th>هدف</th><th>زمان</th><th>تحویل</th></tr></thead>
   <tbody>{data.history.map(item=><tr key={item.id}><td>{item.kind} · {item.status}</td><td>{item.target}</td><td>{new Date(item.occurred_at).toLocaleString('fa-IR')}</td><td>{item.delivery} ({item.attempts??0})</td></tr>)}</tbody>
  </table></div>}
 </section>
}
