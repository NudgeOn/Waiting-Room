// SPDX-License-Identifier: Apache-2.0
import {defineConfig} from 'vite';
export default defineConfig({root:new URL('.',import.meta.url).pathname,build:{outDir:'../../build/admin-ui',emptyOutDir:true},esbuild:{jsx:'automatic'}});
