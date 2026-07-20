import { BrowserRouter } from 'react-router-dom';
import { AppRoutes } from './routes';
import './styles.css';

export function App(): React.JSX.Element {
  return <BrowserRouter><AppRoutes /></BrowserRouter>;
}
