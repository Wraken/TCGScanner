import React from 'react'
import {createRoot} from 'react-dom/client'
import './style.css'
import App from './App'
import {NoticeProvider} from './components/Notices'

const container = document.getElementById('root')

const root = createRoot(container!)

root.render(
    <React.StrictMode>
        <NoticeProvider>
            <App/>
        </NoticeProvider>
    </React.StrictMode>
)
