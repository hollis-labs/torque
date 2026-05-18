import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { applyTheme, getInitialTheme } from '@hollis-labs/sysop-ui'
import './index.css'
import App from './App.tsx'

applyTheme(getInitialTheme())

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
