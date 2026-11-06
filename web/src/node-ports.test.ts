import {expect,it} from 'vitest';
import {checkedPorts,defaultPorts} from './node-ports';
it('rejects shared panel listeners and duplicate/invalid ports before installing',()=>{
 const p=defaultPorts(true);
 for(const reality of [80,443,9443,8443,-1,65536,1.5])expect(()=>checkedPorts({...p,reality},true)).toThrow();
 expect(checkedPorts({...p,reality:12087},true).reality).toBe(12087);
});
