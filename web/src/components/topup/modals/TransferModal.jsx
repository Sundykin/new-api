/*
Copyright (C) 2025 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/

import React, { useState } from 'react';
import { Modal, Typography, Input, InputNumber } from '@douyinfe/semi-ui';
import { CreditCard } from 'lucide-react';
import {
  renderQuota as renderQuotaHelper,
  getCurrencyConfig,
} from '../../../helpers';
import {
  quotaToDisplayAmount,
  displayAmountToQuota,
} from '../../../helpers/quota';

const { Text } = Typography;

const TransferModal = ({
  t,
  openTransfer,
  transfer,
  handleTransferCancel,
  userState,
  renderQuota,
  getQuotaPerUnit,
  transferAmount,
  setTransferAmount,
}) => {
  const [amountLocal, setAmountLocal] = useState('');
  const currencyConfig = getCurrencyConfig();
  const showCurrency = currencyConfig.type !== 'TOKENS';
  const affQuota = userState?.user?.aff_quota || 0;
  const minQuota = getQuotaPerUnit();

  const handleAmountChange = (val) => {
    setAmountLocal(val);
    if (val != null && val !== '') {
      setTransferAmount(displayAmountToQuota(Math.abs(val)));
    } else {
      setTransferAmount(minQuota);
    }
  };

  const handleQuotaChange = (val) => {
    setTransferAmount(val);
    if (showCurrency && val != null && val !== '') {
      setAmountLocal(
        Number(quotaToDisplayAmount(Math.abs(val)).toFixed(2)),
      );
    } else {
      setAmountLocal('');
    }
  };

  // Transfer all
  const handleTransferAll = () => {
    setTransferAmount(affQuota);
    if (showCurrency) {
      setAmountLocal(
        Number(quotaToDisplayAmount(affQuota).toFixed(2)),
      );
    }
  };

  return (
    <Modal
      title={
        <div className='flex items-center'>
          <CreditCard className='mr-2' size={18} />
          {t('划转邀请额度')}
        </div>
      }
      visible={openTransfer}
      onOk={() => {
        transfer();
        setAmountLocal('');
      }}
      onCancel={() => {
        handleTransferCancel();
        setAmountLocal('');
      }}
      maskClosable={false}
      centered
    >
      <div className='space-y-4'>
        <div>
          <Text strong className='block mb-2'>
            {t('可用邀请额度')}
          </Text>
          <Input
            value={renderQuota(affQuota)}
            disabled
            className='!rounded-lg'
          />
        </div>

        <div className='mb-4'>
          <Text type='secondary' className='block mb-2'>
            {`${t('划转后余额：')}${renderQuota(affQuota)} - ${renderQuota(transferAmount || 0)} = ${renderQuota(Math.max(0, affQuota - (transferAmount || 0)))}`}
          </Text>
        </div>

        {showCurrency && (
          <div>
            <div className='mb-1'>
              <Text size='small'>{t('金额')}</Text>
              <Text size='small' type='tertiary'>
                {' '}
                ({t('仅用于换算，实际保存的是额度')})
              </Text>
            </div>
            <InputNumber
              prefix={currencyConfig.symbol}
              placeholder={t('输入金额')}
              value={amountLocal}
              precision={2}
              onChange={handleAmountChange}
              style={{ width: '100%' }}
              showClear
            />
          </div>
        )}

        <div>
          <div className='flex items-center justify-between mb-1'>
            <div>
              <Text size='small'>{t('额度')}</Text>
              <Text size='small' type='tertiary'>
                {' · '}
                {t('最低') + renderQuota(minQuota)}
              </Text>
            </div>
            <Text
              size='small'
              link
              onClick={handleTransferAll}
              style={{ cursor: 'pointer' }}
            >
              {t('全部划转')}
            </Text>
          </div>
          <InputNumber
            min={minQuota}
            max={affQuota}
            value={transferAmount}
            onChange={handleQuotaChange}
            style={{ width: '100%' }}
            showClear
            step={500000}
          />
        </div>
      </div>
    </Modal>
  );
};

export default TransferModal;
