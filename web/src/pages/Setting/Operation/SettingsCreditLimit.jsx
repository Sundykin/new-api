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

import React, { useEffect, useState, useRef } from 'react';
import { Button, Col, Form, Row, Spin, Typography } from '@douyinfe/semi-ui';
import { useTranslation } from 'react-i18next';
import {
  compareObjects,
  API,
  showError,
  showSuccess,
  showWarning,
} from '../../../helpers';
import { toBoolean } from '../../../helpers/boolean';

const DEFAULT_INPUTS = {
  QuotaForNewUser: '',
  PreConsumedQuota: '',
  QuotaForInviter: '',
  QuotaForInvitee: '',
  InviterRebateEnabled: false,
  InviterRebateMode: 'fixed',
  InviterRebateFixedQuota: '',
  InviterRebateRatio: '',
  InviterRebateSubscriptionRatio: '',
  InviterRebateMinConsume: '',
  'quota_setting.enable_free_model_pre_consume': true,
};
const INPUT_KEYS = Object.keys(DEFAULT_INPUTS);
const BOOLEAN_KEYS = INPUT_KEYS.filter(
  (k) => typeof DEFAULT_INPUTS[k] === 'boolean',
);

export default function SettingsCreditLimit(props) {
  const { t } = useTranslation();
  const [loading, setLoading] = useState(false);
  const [inputs, setInputs] = useState(DEFAULT_INPUTS);
  const refForm = useRef();
  const [inputsRow, setInputsRow] = useState(inputs);

  function onSubmit() {
    const updateArray = compareObjects(inputs, inputsRow);
    if (!updateArray.length) return showWarning(t('你似乎并没有修改什么'));
    const requestQueue = updateArray.map((item) => {
      let value = '';
      if (typeof inputs[item.key] === 'boolean') {
        value = String(inputs[item.key]);
      } else {
        value = inputs[item.key];
      }
      return API.put('/api/option/', {
        key: item.key,
        value,
      });
    });
    setLoading(true);
    Promise.all(requestQueue)
      .then((res) => {
        if (requestQueue.length === 1) {
          if (res.includes(undefined)) return;
        } else if (requestQueue.length > 1) {
          if (res.includes(undefined))
            return showError(t('部分保存失败，请重试'));
        }
        showSuccess(t('保存成功'));
        props.refresh();
      })
      .catch(() => {
        showError(t('保存失败，请重试'));
      })
      .finally(() => {
        setLoading(false);
      });
  }

  useEffect(() => {
    const currentInputs = { ...DEFAULT_INPUTS };
    for (let key of INPUT_KEYS) {
      if (key in props.options) {
        currentInputs[key] = BOOLEAN_KEYS.includes(key)
          ? toBoolean(props.options[key])
          : props.options[key];
      }
    }
    setInputs(currentInputs);
    setInputsRow(structuredClone(currentInputs));
    // Delay setValues to ensure conditionally-rendered fields are mounted
    setTimeout(() => {
      refForm.current?.setValues(currentInputs);
    }, 0);
  }, [props.options]);

  // Re-sync Form values when conditional sections mount/unmount
  useEffect(() => {
    refForm.current?.setValues(inputs);
  }, [inputs.InviterRebateEnabled, inputs.InviterRebateMode]);

  return (
    <>
      <Spin spinning={loading}>
        <Form
          values={inputs}
          getFormApi={(formAPI) => (refForm.current = formAPI)}
          style={{ marginBottom: 15 }}
        >
          <Form.Section text={t('额度设置')}>
            <Row gutter={16}>
              <Col xs={24} sm={12} md={8} lg={8} xl={8}>
                <Form.InputNumber
                  label={t('新用户初始额度')}
                  field={'QuotaForNewUser'}
                  step={1}
                  min={0}
                  suffix={'Token'}
                  placeholder={''}
                  onChange={(value) =>
                    setInputs({
                      ...inputs,
                      QuotaForNewUser: String(value),
                    })
                  }
                />
              </Col>
              <Col xs={24} sm={12} md={8} lg={8} xl={8}>
                <Form.InputNumber
                  label={t('请求预扣费额度')}
                  field={'PreConsumedQuota'}
                  step={1}
                  min={0}
                  suffix={'Token'}
                  extraText={t('请求结束后多退少补')}
                  placeholder={''}
                  onChange={(value) =>
                    setInputs({
                      ...inputs,
                      PreConsumedQuota: String(value),
                    })
                  }
                />
              </Col>
              <Col xs={24} sm={12} md={8} lg={8} xl={8}>
                <Form.InputNumber
                  label={t('邀请新用户奖励额度')}
                  field={'QuotaForInviter'}
                  step={1}
                  min={0}
                  suffix={'Token'}
                  extraText={''}
                  placeholder={t('例如：2000')}
                  onChange={(value) =>
                    setInputs({
                      ...inputs,
                      QuotaForInviter: String(value),
                    })
                  }
                />
              </Col>
            </Row>
            <Row>
              <Col xs={24} sm={12} md={8} lg={8} xl={6}>
                <Form.InputNumber
                  label={t('新用户使用邀请码奖励额度')}
                  field={'QuotaForInvitee'}
                  step={1}
                  min={0}
                  suffix={'Token'}
                  extraText={''}
                  placeholder={t('例如：1000')}
                  onChange={(value) =>
                    setInputs({
                      ...inputs,
                      QuotaForInvitee: String(value),
                    })
                  }
                />
              </Col>
            </Row>
            <Row gutter={16}>
              <Col xs={24}>
                <Typography.Title
                  heading={6}
                  style={{ marginTop: 12, marginBottom: 4 }}
                >
                  {t('邀请返利设置')}
                </Typography.Title>
              </Col>
              <Col xs={24} sm={12} md={8} lg={8} xl={8}>
                <Form.Switch
                  label={t('启用邀请返利')}
                  field={'InviterRebateEnabled'}
                  extraText={t('被邀请用户充值后给邀请人返利')}
                  onChange={(value) =>
                    setInputs({
                      ...inputs,
                      InviterRebateEnabled: value,
                    })
                  }
                />
              </Col>
            </Row>
            {inputs.InviterRebateEnabled && (
              <>
                <Row gutter={16}>
                  <Col xs={24} sm={12} md={8} lg={8} xl={8}>
                    <Form.RadioGroup
                      label={t('返利模式')}
                      field={'InviterRebateMode'}
                      type='button'
                      initValue={inputs.InviterRebateMode || 'fixed'}
                      onChange={(e) =>
                        setInputs({
                          ...inputs,
                          InviterRebateMode: e.target.value,
                        })
                      }
                      options={[
                        { label: t('固定额度'), value: 'fixed' },
                        { label: t('按比例'), value: 'ratio' },
                      ]}
                    />
                  </Col>
                  {inputs.InviterRebateMode === 'fixed' && (
                    <Col xs={24} sm={12} md={8} lg={8} xl={8}>
                      <Form.InputNumber
                        label={t('充值返利固定额度')}
                        field={'InviterRebateFixedQuota'}
                        step={1}
                        min={0}
                        suffix={'Token'}
                        placeholder={t('例如：50000')}
                        onChange={(value) =>
                          setInputs({
                            ...inputs,
                            InviterRebateFixedQuota: String(value),
                          })
                        }
                      />
                    </Col>
                  )}
                  {inputs.InviterRebateMode === 'ratio' && (
                    <Col xs={24} sm={12} md={8} lg={8} xl={8}>
                      <Form.InputNumber
                        label={t('充值返利比例')}
                        field={'InviterRebateRatio'}
                        step={0.01}
                        min={0}
                        max={1}
                        placeholder={t('例如：0.1 表示 10%')}
                        onChange={(value) =>
                          setInputs({
                            ...inputs,
                            InviterRebateRatio: String(value),
                          })
                        }
                      />
                    </Col>
                  )}
                </Row>
                <Row gutter={16}>
                  <Col xs={24} sm={12} md={8} lg={8} xl={8}>
                    <Form.InputNumber
                      label={t('订阅套餐返利比例')}
                      field={'InviterRebateSubscriptionRatio'}
                      step={0.01}
                      min={0}
                      max={1}
                      extraText={t('按订阅套餐总额度的比例返利给邀请人，0为不返利')}
                      placeholder={t('例如：0.1 表示 10%')}
                      onChange={(value) =>
                        setInputs({
                          ...inputs,
                          InviterRebateSubscriptionRatio: String(value),
                        })
                      }
                    />
                  </Col>
                </Row>
                <Row gutter={16}>
                  <Col xs={24} sm={12} md={8} lg={8} xl={8}>
                    <Form.InputNumber
                      label={t('返利最低消费额度')}
                      field={'InviterRebateMinConsume'}
                      step={1}
                      min={0}
                      suffix={'Token'}
                      extraText={t('被邀请用户消费达到此额度后才给邀请人返利，0为不限制')}
                      placeholder={'0'}
                      onChange={(value) =>
                        setInputs({
                          ...inputs,
                          InviterRebateMinConsume: String(value),
                        })
                      }
                    />
                  </Col>
                </Row>
              </>
            )}

            <Row>
              <Col>
                <Form.Switch
                  label={t('对免费模型启用预消耗')}
                  field={'quota_setting.enable_free_model_pre_consume'}
                  extraText={t(
                    '开启后，对免费模型（倍率为0，或者价格为0）的模型也会预消耗额度',
                  )}
                  onChange={(value) =>
                    setInputs({
                      ...inputs,
                      'quota_setting.enable_free_model_pre_consume': value,
                    })
                  }
                />
              </Col>
            </Row>

            <Row>
              <Button size='default' onClick={onSubmit}>
                {t('保存额度设置')}
              </Button>
            </Row>
          </Form.Section>
        </Form>
      </Spin>
    </>
  );
}
