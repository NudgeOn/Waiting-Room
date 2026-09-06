// SPDX-License-Identifier: Apache-2.0
import React from 'react';
import {navigate} from './routes.js';
export function Link({to,children,...props}){return <a href={to} {...props} onClick={e=>{if(e.button===0&&!e.metaKey&&!e.ctrlKey&&!e.shiftKey&&!e.altKey){e.preventDefault();navigate(to);}}}>{children}</a>;}
export function RoomTabs({id,tab}){return <nav className="workspace-nav" aria-label="Room 화면">{[['operations','운영'],['settings','설정'],['schedule','일정'],['verification','검증']].map(([key,label])=><Link key={key} to={`/rooms/${id}/${key}`} aria-current={key===tab?'page':undefined}>{label}</Link>)}</nav>;}
