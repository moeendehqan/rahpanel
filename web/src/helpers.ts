export function bytes(n:number):string{if(n>=1073741824)return (n/1073741824).toLocaleString('fa-IR',{maximumFractionDigits:1})+' GB';if(n>=1048576)return (n/1048576).toLocaleString('fa-IR',{maximumFractionDigits:1})+' MB';return n.toLocaleString('fa-IR')+' B'}
export function online(n:{enabled:boolean;ready:boolean;last_seen:string},now=Date.now()):boolean{return n.enabled&&n.ready&&now-Date.parse(n.last_seen)<300000}
const dayMs=86400000;
export const expiryTimeZone='Asia/Tehran';
const persianCalendar=new Intl.DateTimeFormat('fa-IR-u-ca-persian-nu-latn',{
  timeZone:expiryTimeZone,year:'numeric',month:'2-digit',day:'2-digit',
});

export function persianDate(date:Date){
  return persianCalendar.format(date).padStart(10,'0');
}

function normalizeDigits(input:string){
  return input.trim().replace(/[۰-۹٠-٩]/g,digit=>String('۰۱۲۳۴۵۶۷۸۹٠١٢٣٤٥٦٧٨٩'.indexOf(digit)%10));
}

export function expiryFromPersianDate(input:string){
  const match=/^(\d{4})[/-](\d{1,2})[/-](\d{1,2})$/.exec(normalizeDigits(input));
  const [year,month,day]=match ? match.slice(1).map(Number) : [0,0,0];
  if(year>=1&&year<=9378&&month>=1&&month<=12&&day>=1&&day<=31){
    const target=year*10000+month*100+day;
    // Find the last UTC second that still belongs to this date in Tehran.
    let first=Date.UTC(year+621,0,1)/1000;
    let last=Date.UTC(Math.min(year+623,10000),0,1)/1000-1;
    let end:Date|undefined;
    while(first<=last){
      const middle=Math.floor((first+last)/2);
      const instant=new Date(middle*1000);
      const [y,m,d]=persianDate(instant).split('/').map(Number);
      const candidate=y*10000+m*100+d;
      if(candidate<=target){
        if(candidate===target)end=instant;
        first=middle+1;
      }else last=middle-1;
    }
    if(end)return end.toISOString();
  }
  throw new Error('تاریخ شمسی معتبری به شکل ۱۴۰۵/۰۸/۱۹ وارد کنید.');
}

export function expiryFromDays(days:string,now=Date.now()){
  const normalized=normalizeDigits(days);
  if(!/^\d+$/.test(normalized))return undefined;
  const count=Number(normalized);
  const expiry=now+count*dayMs;
  if(!Number.isSafeInteger(count)||count<1||expiry>=Date.UTC(10000,0,1))return undefined;
  return new Date(expiry).toISOString();
}

export function userPayload(name:string,quota:string,expiry:string,group:string){const value=Number(quota);if(!name.trim()||!Number.isFinite(value)||value<0||value>1000000||!Number.isFinite(Date.parse(expiry))||!group)throw new Error('نام، حجم و تاریخ معتبر وارد کنید');return {name:name.trim(),quota_bytes:Math.round(value*1073741824),expires_at:expiry,group_id:group,enabled:true}}
