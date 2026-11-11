import {describe,expect,it} from 'vitest';
import {installationProgress} from './install-jobs';

describe('installation progress',()=>{
 it.each([
  ['preflight',0],['ssh',0],['dns',1],['certificate',2],['identity',3],
  ['artifacts',3],['install',3],['nginx',3],['verify',4],
 ])('maps %s to stage %i',(step,current)=>{
  const progress=installationProgress({step,status:'running'})!;
  expect(progress.findIndex(stage=>stage.active)).toBe(current);
  expect(progress.map(stage=>stage.state)).toEqual(
   progress.map((_,index)=>index<current?'انجام‌شده':index===current?'در حال اجرا':'در انتظار'),
  );
 });

 it.each(['queued','interrupted'])('shows waiting stages for a %s job that never started',status=>{
  expect(installationProgress({step:'queued',status})?.every(stage=>!stage.active && stage.state==='در انتظار')).toBe(true);
 });

 it.each([['ssh',0],['certificate',2]])('marks failure on %s',(step,index)=>{
  expect(installationProgress({step,status:'failed'})?.[index]).toMatchObject({state:'ناموفق',showMessage:true});
 });

 it('marks the last active stage when interrupted',()=>{
  expect(installationProgress({step:'install',status:'interrupted'})?.[3]).toMatchObject({state:'متوقف‌شده',showMessage:true});
 });

 it('keeps the final stage completed when it has a warning',()=>{
  const progress=installationProgress({step:'ready',status:'completed'})!;
  expect(progress.every(stage=>stage.state==='انجام‌شده')).toBe(true);
  expect(progress.filter(stage=>stage.showMessage)).toEqual([progress[4]]);
 });

 it.each([['future-step','running'],['future-step','queued'],['install','future-status']])('falls back for %s with %s',(step,status)=>{
  expect(installationProgress({step,status})).toBeNull();
 });
});
