import {useId,useState} from 'react';
import {ChevronLeft,Clock} from 'lucide-react';
import type {Job,Node} from './types';

const stages = [
 {label:'آماده‌سازی و اتصال SSH', steps:['preflight','ssh'], hint:'تنظیمات پنل، دسترسی SSH و پیش‌نیازهای میزبان را بررسی کنید.'},
 {label:'تنظیم DNS', steps:['dns'], hint:'تنظیمات دامنه و مجوزهای توکن Cloudflare را بررسی کنید.'},
 {label:'صدور گواهی TLS', steps:['certificate'], hint:'تنظیمات DNS، مجوزهای Cloudflare و دسترسی به سرویس صدور گواهی را بررسی کنید.'},
 {label:'آماده‌سازی و راه‌اندازی Xray و عامل', steps:['identity','artifacts','install','nginx'], hint:'پیش‌نیازهای میزبان و آزاد بودن پورت‌های انتخاب‌شده را بررسی کنید.'},
 {label:'آزمون اتصال', steps:['verify'], hint:'فایروال، DNS و وضعیت عامل سرور را بررسی کنید.'},
];

const statusLabels = new Map([
 ['queued','در صف'], ['running','در حال اجرا'], ['completed','تکمیل‌شده'],
 ['failed','ناموفق'], ['interrupted','متوقف‌شده'],
]);

export function installationProgress({step,status}: Pick<Job,'step'|'status'>) {
 const current = stages.findIndex(stage=>stage.steps.includes(step));
 const queued = step==='queued' && (status==='queued' || status==='interrupted');
 const completed = status==='completed' && step==='ready';
 if (!statusLabels.has(status) || (!queued && !completed && current===-1)) return null;

 return stages.map((stage,index)=>{
  const active = !queued && !completed && index===current;
  const state = completed || (!queued && index<current) ? 'انجام‌شده'
   : active ? statusLabels.get(status) : 'در انتظار';
  return {...stage, state, active, showMessage:active || (completed && index===stages.length-1)};
 });
}

function InstallJob({job,name,defaultOpen}: {job:Job;name:string;defaultOpen:boolean}) {
 const [openChoice,setOpenChoice] = useState<boolean>();
 const id = useId();
 const open = openChoice ?? defaultOpen;
 const progress = installationProgress(job);
 const hint = job.status==='interrupted'
  ? 'برای تلاش دوباره، از صفحهٔ سرورها فرم نصب را باز کنید و اطلاعات SSH را دوباره وارد کنید.'
  : job.step==='ssh' ? 'دسترسی SSH و اعتبارنامهٔ سرور را بررسی کنید.' : undefined;
 const showSummaryError = !progress?.some(stage=>stage.showMessage);

 return <article className="install-job">
  <button type="button" className="job install-job-toggle" aria-expanded={open} aria-controls={id}
   onClick={()=>setOpenChoice(!open)}>
   <span className="job-icon" aria-hidden="true"><Clock size={20}/></span>
   <span className="install-job-name">
    <strong>{name}</strong>
    <small><time dateTime={job.updated_at}>{new Date(job.updated_at).toLocaleString('fa-IR')}</time></small>
   </span>
   <span className={'badge '+(job.status==='completed'?'good':'neutral')}><bdi>{statusLabels.get(job.status) ?? job.status}</bdi></span>
   <ChevronLeft size={16} aria-hidden="true"/>
  </button>
  <div id={id} hidden={!open} className="install-job-details">
   {progress ? <ol className="install-job-stages">
    {progress.map(stage=><li key={stage.label} aria-current={stage.active?'step':undefined}>
     <div className="install-job-stage"><strong>{stage.label}</strong><span>{stage.state}</span></div>
     {stage.showMessage && job.error && <div className="install-job-message">
      {job.status==='completed' && <strong>هشدار</strong>}
      <p dir="auto">{job.error}</p>
      {job.status!=='completed' && <p>{hint ?? stage.hint}</p>}
     </div>}
    </li>)}
   </ol> : <p>مرحلهٔ ثبت‌شده <bdi>{job.step}</bdi></p>}
   {showSummaryError && job.error && <div className="install-job-message">
    <p dir="auto">{job.error}</p>
    {hint && <p>{hint}</p>}
   </div>}
  </div>
 </article>;
}

export function InstallJobs({jobs,nodes}: {jobs:Job[];nodes:Node[]}) {
 const newestFailure = jobs.reduce<Job|undefined>((latest,job)=>
  job.status==='failed' && (!latest || Date.parse(job.updated_at)>Date.parse(latest.updated_at)) ? job : latest,
 undefined);
 const names = new Map(nodes.map(node=>[node.id,node.name]));

 return <section className="card">
  <div className="section-heading"><h2>آخرین عملیات</h2><span className="muted">به‌روزرسانی هر ۱۰ ثانیه</span></div>
  {jobs.map(job=><InstallJob key={job.id} job={job} name={names.get(job.node_id) || job.node_id}
   defaultOpen={job.status==='queued' || job.status==='running' || job.id===newestFailure?.id}/>)}
 </section>;
}
