// SPDX-License-Identifier: Apache-2.0
import AxeBuilder from '@axe-core/playwright';
import {expect} from '@playwright/test';
export async function accessibility(page){
 const result=await new AxeBuilder({page}).withTags(['wcag2a','wcag2aa','wcag21a','wcag21aa','wcag22aa']).analyze();
 // Selectors only. Never emit HTML, passwords, OTP secrets or recovery codes.
 expect(result.violations.map(v=>({id:v.id,targets:v.nodes.map(n=>n.target)}))).toEqual([]);
}
