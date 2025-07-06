import React from 'react'
import ReactDOM from 'react-dom/client'
import App from './App.tsx'
import './styles/index.css'

// Initialize PrismJS
import 'prismjs/themes/prism.css'
import 'prismjs/components/prism-core'
import 'prismjs/plugins/autoloader/prism-autoloader'

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
)