// SPDX-License-Identifier: Apache-2.0
import React from 'react';
import {navigate} from './routes.js';
export function Link({to,children,...props}){return <a href={to} {...props} onClick={e=>{if(e.button===0&&!e.metaKey&&!e.ctrlKey&&!e.shiftKey&&!e.altKey){e.preventDefault();navigate(to);}}}>{children}</a>;}
export function RoomTabs({id,tab}){return <nav className="workspace-nav" aria-label="대기열 화면">{[['operations','입장 운영'],['settings','설정'],['schedule','입장 예약'],['verification','연결 확인']].map(([key,label])=><Link key={key} to={`/rooms/${id}/${key}`} aria-current={key===tab?'page':undefined}>{label}</Link>)}</nav>;}
