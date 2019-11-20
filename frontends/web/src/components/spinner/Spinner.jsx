/**
 * Copyright 2018 Shift Devices AG
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *      http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

import { Component, h } from 'preact';
import * as style from './Spinner.css';
import MenuIcon from '../../assets/icons/menu.svg';
import { toggleSidebar } from '../sidebar/sidebar';

export default class Spinner extends Component {
    componentWillMount() {
        document.addEventListener('keydown', this.handleKeyDown);
    }

    componentWillUnmount() {
        document.removeEventListener('keydown', this.handleKeyDown);
    }

    handleKeyDown = e => {
        e.preventDefault();
        // @ts-ignore (blur exists only on HTMLElements)
        document.activeElement.blur();
    }

    render({
        text,
    }, {}) {
        return (
            <div className={style.spinnerContainer}>
                <div className={style.togglerContainer}>
                    <div className={style.toggler} onClick={toggleSidebar}>
                        <img src={MenuIcon} />
                    </div>
                </div>
                {
                    text && (
                        <p className={style.spinnerText}>{text}</p>
                    )
                }
                <div className={style.spinner}>
                    <div></div>
                    <div></div>
                    <div></div>
                    <div></div>
                </div>
                <div className={style.overlay}></div>
            </div>
        );
    }
}
