import pandas as pd

df = pd.read_csv("log.csv")
print(df['profit_cad'].sum())
