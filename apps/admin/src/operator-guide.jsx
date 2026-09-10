// SPDX-License-Identifier: Apache-2.0
import React from 'react';
import {Link} from './navigation.jsx';

export default function OperatorGuide({current,roomId}){
 const steps=[['설정 준비','주소와 입장 인원을 저장해요.',roomId?`/rooms/${roomId}/settings`:'/rooms'],['서비스에 적용','저장한 설정을 실제 서비스에 반영해요.','/dashboard/runtime'],['입장 시작','대기 중인 사람을 순서대로 들여보내요.',roomId?`/rooms/${roomId}/operations`:'/']];
 return <nav className="operator-guide" aria-label="운영 시작 순서">{steps.map(([title,description,to],index)=><Link key={title} to={to} aria-current={current===index+1?'step':undefined}><strong>{index+1}. {title}</strong><span>{description}</span></Link>)}</nav>;
}
