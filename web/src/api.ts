export async function api<T>(path:string,method='GET',body?:unknown):Promise<T>{
 const res=await fetch('/api/v1'+path,{method,credentials:'same-origin',headers:body?{'Content-Type':'application/json'}:{},body:body?JSON.stringify(body):undefined});
 const data=await res.json();if(!res.ok)throw new Error(data.error||'ارتباط با پنل برقرار نشد');return data as T;
}
