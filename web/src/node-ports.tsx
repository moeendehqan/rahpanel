import type {NodePorts} from './types';

export function defaultPorts(shared=false):NodePorts{return {reality:shared?2087:443,websocket:8443,trojan:2053,stats:shared?10086:10085}}
export function checkedPorts(ports:NodePorts,shared:boolean):NodePorts{
 const values=Object.values(ports);
 if(values.length!==4||values.some(p=>!Number.isInteger(p)||p<1||p>65535))throw new Error('پورت‌ها باید عدد صحیح بین ۱ و ۶۵۵۳۵ باشند.');
 if(new Set(values).size!==4)throw new Error('برای هر روش و آمار داخلی، پورت جدا انتخاب کنید.');
 if(shared&&values.some(p=>[80,443,9443].includes(p)))throw new Error('پورت‌های ۸۰، ۴۴۳ و ۹۴۴۳ برای پنل محفوظ‌اند.');
 return ports;
}
export function PortFields({ports,onChange,disabled=false}:{ports:NodePorts;onChange:(p:NodePorts)=>void;disabled?:boolean}){
 return <details><summary>پورت‌های اتصال (تنظیم اختیاری)</summary><div className="form-grid">{([['reality','پورت REALITY'],['websocket','پورت WebSocket TLS'],['trojan','پورت Trojan TLS'],['stats','پورت آمار داخلی']] as const).map(([key,label])=><label className="field" key={key}><span>{label}</span><input dir="ltr" type="number" min="1" max="65535" value={ports[key]} disabled={disabled} onChange={e=>onChange({...ports,[key]:Number(e.target.value)})} required/></label>)}</div></details>
}
