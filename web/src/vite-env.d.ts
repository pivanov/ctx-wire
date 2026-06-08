/// <reference types="vite/client" />

// Fontsource packages export CSS via their package "exports" field.
// TypeScript does not ship .d.ts for these; declare them as side-effect modules.
declare module "@fontsource-variable/bricolage-grotesque" {}
declare module "@fontsource-variable/bricolage-grotesque/*.css" {}
declare module "@fontsource/jetbrains-mono" {}
declare module "@fontsource/jetbrains-mono/*.css" {}
