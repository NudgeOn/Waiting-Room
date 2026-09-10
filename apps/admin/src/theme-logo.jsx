// SPDX-License-Identifier: Apache-2.0
import React,{useEffect,useId,useRef,useState} from 'react';
import {LOGO_HELP,logoPreviewURL,readLogoFile} from './theme-logo.js';
import './theme-logo.css';

export default function ThemeLogo({value,onChange,onLoadingChange,disabled}){
  const id=useId(),version=useRef(0),input=useRef(null),[error,setError]=useState(''),[loading,setLoading]=useState(false);
  useEffect(()=>()=>{version.current++;},[]);
  async function select(event){
    const file=event.target.files?.[0],current=++version.current;if(!file)return;
    setLoading(true);onLoadingChange?.(true);setError('');
    try{const encoded=await readLogoFile(file);if(current===version.current)onChange(encoded);}
    catch(e){if(current===version.current)setError(e.message+' 기존 로고는 유지됩니다.');}
    finally{if(current===version.current){setLoading(false);onLoadingChange?.(false);}}
  }
  const preview=logoPreviewURL(value);
  return <div className="theme-logo-field"><label htmlFor={id}>로고 이미지 (선택)</label><input ref={input} id={id} type="file" accept="image/png,image/jpeg" disabled={disabled||loading} onChange={select} aria-describedby={`${id}-help${error?` ${id}-error`:''}`} aria-invalid={Boolean(error)}/><p id={`${id}-help`}>{LOGO_HELP}</p>{preview?<div className="theme-logo-selection"><img src={preview} alt="선택한 로고 미리보기" width="128" height="64"/><button className="secondary" type="button" disabled={disabled||loading} onClick={()=>{version.current++;onChange('');setError('');if(input.current)input.current.value='';}}>로고 제거</button></div>:null}{loading?<p role="status">로고 확인 중…</p>:null}{error?<p id={`${id}-error`} role="alert">{error}</p>:null}</div>;
}
