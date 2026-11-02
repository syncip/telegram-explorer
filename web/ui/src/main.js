import { mount } from 'svelte'
import './app.css'
import App from './App.svelte'
import { applyTheme } from './theme.js'

applyTheme()

mount(App, { target: document.getElementById('app') })
