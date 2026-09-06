// SPDX-License-Identifier: Apache-2.0
export function parseRoute(path) {
  const fixed={'/':{page:'dashboard'},'/rooms':{page:'rooms'},'/rooms/new':{page:'new'},'/dashboard/runtime':{page:'runtime'},'/settings':{page:'security'},'/auth/session':{page:'session'},'/auth/login':{page:'login'},'/setup':{page:'setup'}};
  if(Object.hasOwn(fixed,path))return fixed[path];
  const match=/^\/rooms\/([a-z][a-z0-9_-]{0,63})(?:\/(operations|settings|schedule|verification))?$/.exec(path);
  return match?{page:'room',roomId:match[1],tab:match[2]??'operations'}:{page:'not-found'};
}
export function navigate(path,{saved=false}={}) {
  if(parseRoute(path).page==='not-found')throw new Error('Unknown console route');
  if(location.pathname!==path){history.pushState(saved?{wrNotice:'draft-saved'}:null,'',path);dispatchEvent(new PopStateEvent('popstate'));}
}
export function subscribeRoute(listener){addEventListener('popstate',listener);return()=>removeEventListener('popstate',listener);}
export function routeSnapshot(){return location.pathname;}
